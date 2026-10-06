package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_object_copy — обёртка над CopyObject: серверная копия объекта,
// без перекачки содержимого через клиента (в отличие от чтения через
// platformcraft_object data source + записи через platformcraft_object resource).
// Управляет жизненным циклом именно копии (объекта-назначения) — источник этим
// ресурсом не трогается и не удаляется.

var (
	_ resource.Resource                = &objectCopyResource{}
	_ resource.ResourceWithConfigure   = &objectCopyResource{}
	_ resource.ResourceWithImportState = &objectCopyResource{}
	_ resource.ResourceWithModifyPlan  = &objectCopyResource{}
)

func NewObjectCopyResource() resource.Resource {
	return &objectCopyResource{}
}

type objectCopyResource struct {
	client *s3.Client
}

type objectCopyResourceModel struct {
	SourceBucket              types.String `tfsdk:"source_bucket"`
	SourceKey                 types.String `tfsdk:"source_key"`
	Bucket                    types.String `tfsdk:"bucket"`
	Key                       types.String `tfsdk:"key"`
	Etag                      types.String `tfsdk:"etag"`
	BypassGovernanceRetention types.Bool   `tfsdk:"bypass_governance_retention"`
	Id                        types.String `tfsdk:"id"`
}

func (r *objectCopyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_copy"
}

func (r *objectCopyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Серверная копия объекта PlatformCraft S3 (copy-object) — без скачивания содержимого через клиента. " +
			"Управляет объектом-назначением (bucket/key); источник (source_bucket/source_key) этим ресурсом не изменяется и не удаляется.",
		Attributes: map[string]schema.Attribute{
			"source_bucket": schema.StringAttribute{
				Required:    true,
				Description: "Бакет-источник.",
			},
			"source_key": schema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта-источника.",
			},
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Бакет-назначение.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта-назначения.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"etag": schema.StringAttribute{
				Computed:    true,
				Description: "ETag копии-назначения после copy-object.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"bypass_governance_retention": schema.BoolAttribute{
				Optional:    true,
				Description: "Если true, при удалении копии, защищённой Object Lock в режиме GOVERNANCE, защита обходится. Копия получает retention по умолчанию бакета-назначения (platformcraft_bucket_object_lock_configuration), даже если retention для неё явно не задан. По умолчанию false: удаление защищённой копии завершается ошибкой API.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор ресурса в Terraform state (bucket/key назначения).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *objectCopyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *objectCopyResource) doCopy(ctx context.Context, plan objectCopyResourceModel) error {
	return pcs3.CopyObject(ctx, r.client,
		plan.SourceBucket.ValueString(), plan.SourceKey.ValueString(),
		plan.Bucket.ValueString(), plan.Key.ValueString())
}

func (r *objectCopyResource) refreshEtag(ctx context.Context, plan *objectCopyResourceModel) error {
	head, err := pcs3.HeadObject(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString())
	if err != nil {
		return err
	}
	plan.Etag = types.StringValue(aws.ToString(head.ETag))
	return nil
}

func (r *objectCopyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectCopyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.doCopy(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка копирования объекта", err.Error())
		return
	}
	if err := r.refreshEtag(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Ошибка чтения ETag копии после copy-object", err.Error())
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectCopyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectCopyResourceModel
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
		resp.Diagnostics.AddError("Ошибка проверки объекта-копии", err.Error())
		return
	}

	// Мы не сверяем ETag копии с текущим ETag источника на каждый Read — источник
	// мог измениться уже после copy-object, а "дрифт" здесь означает не более
	// актуальности содержимого-назначения относительно момента копирования; чтобы
	// пересоздать копию под свежий источник, поменяйте source_key/source_bucket
	// либо явно удалите и создайте ресурс заново (terraform taint).
	state.Etag = types.StringValue(aws.ToString(head.ETag))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *objectCopyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state objectCopyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// bucket/key назначения помечены RequiresReplace. Перекопируем, только если
	// поменялся источник; смена bypass_governance_retention касается лишь Delete.
	if objectCopySourceChanged(plan, state) {
		if err := r.doCopy(ctx, plan); err != nil {
			resp.Diagnostics.AddError("Ошибка перекопирования объекта", err.Error())
			return
		}
		if err := r.refreshEtag(ctx, &plan); err != nil {
			resp.Diagnostics.AddError("Ошибка чтения ETag копии после copy-object", err.Error())
			return
		}
	} else {
		plan.Etag = state.Etag
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func objectCopySourceChanged(plan, state objectCopyResourceModel) bool {
	return !plan.SourceBucket.Equal(state.SourceBucket) || !plan.SourceKey.Equal(state.SourceKey)
}

// ModifyPlan помечает etag как «known after apply», когда меняется источник
// копии: объект будет перезаписан, и старый etag в плане не совпал бы с
// фактическим ("Provider produced inconsistent result after apply").
func (r *objectCopyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state objectCopyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if objectCopySourceChanged(plan, state) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("etag"), types.StringUnknown())...)
	}
}

func (r *objectCopyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectCopyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Удаляем только назначение (копию) — источник этим ресурсом не управляется.
	// bypass_governance_retention по умолчанию false — как и у platformcraft_object,
	// удаление залоченной копии явно падает с ошибкой API, а не тихо обходит защиту.
	//
	// Отдельно от retention: copy-object может унаследовать legal hold от объекта-
	// источника (например, если на нём уже стоит platformcraft_object_legal_hold),
	// а у самой копии в Terraform для этого нет ни ресурса, ни поля — источник
	// legal hold мы не отслеживаем. bypass_governance_retention тут не помогает:
	// это две независимые блокировки Object Lock, флаг обходит только retention.
	// Поэтому перед удалением проверяем legal hold копии и снимаем его, только
	// если он реально включён. Сначала читаем, а не пишем вслепую: на бакете без
	// Object Lock put-object-legal-hold отвечает ошибкой, и слепая запись
	// заблокировала бы удаление обычной копии. Если чтение не удалось (Object Lock
	// не настроен, объекта уже нет и т.п.) — снимать нечего, сразу удаляем: если
	// блокировка всё же есть, DeleteObject вернёт понятную ошибку API.
	if hold, err := pcs3.GetObjectLegalHold(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString()); err == nil &&
		hold == s3types.ObjectLockLegalHoldStatusOn {
		if err := pcs3.PutObjectLegalHold(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString(), false); err != nil {
			resp.Diagnostics.AddError("Ошибка снятия legal hold с объекта-копии перед удалением", err.Error())
			return
		}
	}

	bypass := !state.BypassGovernanceRetention.IsNull() && state.BypassGovernanceRetention.ValueBool()
	if err := pcs3.DeleteObject(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString(), bypass); err != nil {
		if isNotFound(err) {
			return // бакет уже удалён мимо Terraform
		}
		resp.Diagnostics.AddError("Ошибка удаления объекта-копии", err.Error())
		return
	}
}

func (r *objectCopyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_object_copy.example <bucket>,<key>
	// (bucket/key назначения; source_bucket/source_key после импорта нужно
	// прописать в .tf вручную — Read() их не знает, только адрес назначения.)
	parts := splitTwo(req.ID)
	if parts == nil {
		resp.Diagnostics.AddError(
			"Некорректный формат ID для импорта",
			`Ожидается "<bucket>,<key>", например: terraform import platformcraft_object_copy.example my-bucket,path/to/file.txt`,
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[0]+"/"+parts[1])...)
}
