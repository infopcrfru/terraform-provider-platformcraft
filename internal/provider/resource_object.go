package provider

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	stringvalidator "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

var (
	_ resource.Resource                = &objectResource{}
	_ resource.ResourceWithConfigure   = &objectResource{}
	_ resource.ResourceWithImportState = &objectResource{}
	_ resource.ResourceWithModifyPlan  = &objectResource{}
)

func NewObjectResource() resource.Resource {
	return &objectResource{}
}

type objectResource struct {
	client *s3.Client
}

// content и source_path — задаётся ровно один из двух (валидатор в Schema).
// Multipart upload выбирает manager.Uploader внутри pcs3.PutObject в
// зависимости от размера; отдельной сущностью в Terraform он не является.
type objectResourceModel struct {
	Bucket                    types.String `tfsdk:"bucket"`
	Key                       types.String `tfsdk:"key"`
	Content                   types.String `tfsdk:"content"`
	SourcePath                types.String `tfsdk:"source_path"`
	Acl                       types.String `tfsdk:"acl"`
	Etag                      types.String `tfsdk:"etag"`
	BypassGovernanceRetention types.Bool   `tfsdk:"bypass_governance_retention"`
	RefreshContent            types.Bool   `tfsdk:"refresh_content"`
	Id                        types.String `tfsdk:"id"`
}

func (r *objectResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object"
}

func (r *objectResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Объект в бакете PlatformCraft S3 (put-object/head-object/delete-object). Содержимое задаётся строкой (content) или локальным файлом (source_path); большие файлы автоматически загружаются по частям (multipart upload).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required:    true,
				Description: "Ключ (путь) объекта внутри бакета.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"content": schema.StringAttribute{
				Optional:    true,
				Description: "Содержимое объекта как строка (для небольших текстовых объектов). Взаимоисключимо с source_path.",
				Validators: []validator.String{
					stringvalidator.ExactlyOneOf(
						path.MatchRoot("content"),
						path.MatchRoot("source_path"),
					),
				},
			},
			"source_path": schema.StringAttribute{
				Optional:    true,
				Description: "Путь к локальному файлу, который нужно загрузить как объект. Взаимоисключимо с content.",
			},
			"acl": schema.StringAttribute{
				Optional:    true,
				Description: "Canned ACL объекта: private, public-read, public-read-write или authenticated-read. Если не задан, ACL при загрузке не передаётся.",
				Validators: []validator.String{
					stringvalidator.OneOf("private", "public-read", "public-read-write", "authenticated-read"),
				},
			},
			"etag": schema.StringAttribute{
				Computed:    true,
				Description: "ETag объекта, возвращённый PlatformCraft после загрузки.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bypass_governance_retention": schema.BoolAttribute{
				Optional:    true,
				Description: "Если true, при удалении объекта, защищённого Object Lock в режиме GOVERNANCE, защита обходится (BypassGovernanceRetention); ключам нужно право s3:BypassGovernanceRetention. На режим COMPLIANCE не действует. По умолчанию false: удаление защищённого объекта завершается ошибкой API.",
			},
			"refresh_content": schema.BoolAttribute{
				Optional:    true,
				Description: "Если true, при чтении состояния провайдер скачивает объект и сравнивает содержимое с content, а не только ETag. Так обнаруживаются изменения содержимого вне Terraform, но объект скачивается при каждом plan и refresh. Применяется только вместе с content. По умолчанию false.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор ресурса в Terraform state (bucket/key).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

// ModifyPlan помечает etag как «known after apply», когда меняется содержимое
// объекта. Без этого UseStateForUnknown оставил бы в плане старый etag, и после
// перезаписи объекта Terraform сообщил бы "Provider produced inconsistent result
// after apply" (etag в плане не совпал с фактическим).
func (r *objectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // создание или удаление
	}
	var plan, state objectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Content.Equal(state.Content) || !plan.SourcePath.Equal(state.SourcePath) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("etag"), types.StringUnknown())...)
	}
}

func (r *objectResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*s3.Client)
	if !ok {
		resp.Diagnostics.AddError("Неожиданный тип ProviderData", "Ожидался *s3.Client, это внутренняя ошибка провайдера.")
		return
	}
	r.client = client
}

// bodyReader открывает содержимое объекта из content или source_path.
// Вызывающий код обязан закрыть возвращённый io.ReadCloser, если он не nil
// (для content он всегда nil — strings.Reader закрывать не нужно, поэтому
// используем io.NopCloser только для source_path).
func bodyReader(plan objectResourceModel) (*os.File, *strings.Reader, error) {
	if !plan.SourcePath.IsNull() && plan.SourcePath.ValueString() != "" {
		f, err := os.Open(plan.SourcePath.ValueString())
		if err != nil {
			return nil, nil, err
		}
		return f, nil, nil
	}
	return nil, strings.NewReader(plan.Content.ValueString()), nil
}

func (r *objectResource) putObject(ctx context.Context, plan objectResourceModel) (string, error) {
	file, reader, err := bodyReader(plan)
	if err != nil {
		return "", err
	}
	if file != nil {
		defer file.Close()
		return pcs3.PutObject(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), file)
	}
	return pcs3.PutObject(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), reader)
}

func (r *objectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	etag, err := r.putObject(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка загрузки объекта", err.Error())
		return
	}

	if !plan.Acl.IsNull() && plan.Acl.ValueString() != "" {
		if err := pcs3.PutObjectAcl(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), s3types.ObjectCannedACL(plan.Acl.ValueString())); err != nil {
			if isUnsupportedAclConfiguration(err) {
				resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
				return
			}
			resp.Diagnostics.AddError("Ошибка установки ACL объекта", err.Error())
			return
		}
	}

	plan.Etag = types.StringValue(etag)
	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	head, err := pcs3.HeadObject(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка проверки объекта", err.Error())
		return
	}

	// По умолчанию не перечитываем content/source_path из реального содержимого
	// объекта на каждый Read — это означало бы полное скачивание объекта при
	// каждом terraform plan. Вместо этого сверяем ETag: если он разошёлся с тем,
	// что в state, значит объект поменяли мимо Terraform — это и есть сигнал
	// дрифта, который увидит пользователь, а перекачивать весь файл не нужно.
	// Это не ограничение API — PlatformCraft отдаёт содержимое через обычный
	// GetObject не хуже AWS, — а сознательный компромисс по стоимости/скорости
	// (тот же подход у aws_s3_object в официальном AWS-провайдере). Если он не
	// подходит, можно включить refresh_content=true и получить реальную докачку.
	if head.ETag != nil {
		state.Etag = types.StringValue(aws.ToString(head.ETag))
	}

	if !state.RefreshContent.IsNull() && state.RefreshContent.ValueBool() &&
		(state.SourcePath.IsNull() || state.SourcePath.ValueString() == "") {
		var size int64
		if head.ContentLength != nil {
			size = *head.ContentLength
		}
		if size > maxInlineContentBytes {
			resp.Diagnostics.AddError(
				"Объект слишком большой для refresh_content",
				fmt.Sprintf(
					"Размер объекта %d байт больше безопасного порога %d байт для строкового атрибута state — "+
						"content целиком проходит через gRPC-канал между Terraform core и провайдером (лимит ~256 МиБ "+
						"на сообщение, ограничение terraform-plugin-go, не PlatformCraft). Отключите refresh_content "+
						"для этого объекта: дрифт всё равно будет виден по etag.",
					size, maxInlineContentBytes,
				),
			)
			return
		}

		body, err := pcs3.GetObject(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Ошибка скачивания содержимого объекта (refresh_content=true)", err.Error())
			return
		}
		data, err := io.ReadAll(io.LimitReader(body, maxInlineContentBytes+1))
		closeErr := body.Close()
		if err != nil {
			resp.Diagnostics.AddError("Ошибка чтения содержимого объекта (refresh_content=true)", err.Error())
			return
		}
		if closeErr != nil {
			resp.Diagnostics.AddError("Ошибка закрытия тела ответа объекта (refresh_content=true)", closeErr.Error())
			return
		}
		if int64(len(data)) > maxInlineContentBytes {
			resp.Diagnostics.AddError(
				"Объект оказался больше заявленного размера при refresh_content",
				"Фактическое содержимое превысило безопасный порог уже в процессе скачивания. Отключите refresh_content для этого объекта.",
			)
			return
		}
		state.Content = types.StringValue(string(data))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *objectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan objectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state objectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// content/source_path менялись — перезаливаем объект. bucket/key помечены
	// RequiresReplace, так что Update по ним никогда не вызовется.
	if plan.Content.ValueString() != state.Content.ValueString() || plan.SourcePath.ValueString() != state.SourcePath.ValueString() {
		etag, err := r.putObject(ctx, plan)
		if err != nil {
			resp.Diagnostics.AddError("Ошибка перезаписи объекта", err.Error())
			return
		}
		plan.Etag = types.StringValue(etag)
	} else {
		plan.Etag = state.Etag
	}

	if plan.Acl.ValueString() != state.Acl.ValueString() {
		if !plan.Acl.IsNull() && plan.Acl.ValueString() != "" {
			if err := pcs3.PutObjectAcl(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), s3types.ObjectCannedACL(plan.Acl.ValueString())); err != nil {
				if isUnsupportedAclConfiguration(err) {
					resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
					return
				}
				resp.Diagnostics.AddError("Ошибка изменения ACL объекта", err.Error())
				return
			}
		}
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// bypass_governance_retention управляется полем ресурса (по умолчанию false —
	// если объект защищён Object Lock, удаление явно падает с ошибкой API, а не
	// тихо обходит блокировку). Compliance-режим этим параметром всё равно не
	// обойти — так устроен сам S3, а не провайдер.
	bypass := !state.BypassGovernanceRetention.IsNull() && state.BypassGovernanceRetention.ValueBool()

	if err := pcs3.DeleteObject(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString(), bypass); err != nil {
		if isNotFound(err) {
			return // бакет уже удалён мимо Terraform
		}
		resp.Diagnostics.AddError("Ошибка удаления объекта", err.Error())
		return
	}
}

func (r *objectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_object.example <bucket>,<key>
	// Через запятую, а не через "/" — ключ объекта сам по себе может содержать
	// слэши (пути вида "folder/subfolder/file.txt"), и SplitN по "/" неоднозначно
	// разрезал бы такой ID. Запятая в имени бакета и в ключе объекта недопустима
	// у S3-совместимых хранилищ, поэтому как разделитель она безопасна.
	//
	// После импорта content/source_path останутся пустыми в state — Read() не
	// скачивает содержимое объекта (см. комментарий в Read), поэтому эти поля
	// нужно дозаполнить вручную в .tf, сверив с тем, что реально лежит в бакете,
	// прежде чем следующий apply перезапишет объект.
	parts := strings.SplitN(req.ID, ",", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Некорректный формат ID для импорта",
			`Ожидается "<bucket>,<key>", например: terraform import platformcraft_object.example my-bucket,path/to/file.txt`,
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[0]+"/"+parts[1])...)
}
