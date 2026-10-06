package provider

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

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

// platformcraft_object_retention — retention отдельного объекта, в отличие от
// platformcraft_bucket_object_lock_configuration, который задаёт правило ПО
// УМОЛЧАНИЮ для новых объектов бакета. Позволяет задать объекту срок, отличный
// от бакетного, или назначать retention точечно без бакетного правила.

const retentionTimeLayout = time.RFC3339

var (
	_ resource.Resource                = &objectRetentionResource{}
	_ resource.ResourceWithConfigure   = &objectRetentionResource{}
	_ resource.ResourceWithImportState = &objectRetentionResource{}
)

func NewObjectRetentionResource() resource.Resource {
	return &objectRetentionResource{}
}

type objectRetentionResource struct {
	client *s3.Client
}

type objectRetentionResourceModel struct {
	Bucket                    types.String `tfsdk:"bucket"`
	Key                       types.String `tfsdk:"key"`
	Mode                      types.String `tfsdk:"mode"`
	RetainUntil               types.String `tfsdk:"retain_until"`
	BypassGovernanceRetention types.Bool   `tfsdk:"bypass_governance_retention"`
	Id                        types.String `tfsdk:"id"`
}

func (r *objectRetentionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_retention"
}

func (r *objectRetentionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет retention отдельного объекта (put/get-object-retention), в отличие от platformcraft_bucket_object_lock_configuration, который задаёт правило по умолчанию для новых объектов бакета. Требует включённого Object Lock на бакете. Retention в режиме COMPLIANCE нельзя сократить или снять до retain_until никому, включая владельца аккаунта; GOVERNANCE можно сократить с bypass_governance_retention = true.",
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
				Description: "Ключ объекта.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"mode": schema.StringAttribute{
				Required:    true,
				Description: "GOVERNANCE или COMPLIANCE.",
				Validators: []validator.String{
					stringvalidator.OneOf("GOVERNANCE", "COMPLIANCE"),
				},
			},
			"retain_until": schema.StringAttribute{
				Required:    true,
				Description: "Дата и время окончания retention в формате RFC3339, например \"2026-12-31T00:00:00Z\".",
			},
			"bypass_governance_retention": schema.BoolAttribute{
				Optional:    true,
				Description: "Если true, при изменении ресурса срок GOVERNANCE-retention можно сократить. При удалении ресурса GOVERNANCE-retention снимается независимо от этого флага: PlatformCraft не принимает retain_until в прошлом, поэтому срок переносится на «сейчас + 60 секунд». На COMPLIANCE не действует.",
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

func (r *objectRetentionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *objectRetentionResource) apply(ctx context.Context, plan objectRetentionResourceModel) error {
	retainUntil, err := time.Parse(retentionTimeLayout, plan.RetainUntil.ValueString())
	if err != nil {
		return err
	}
	bypass := !plan.BypassGovernanceRetention.IsNull() && plan.BypassGovernanceRetention.ValueBool()
	return pcs3.PutObjectRetention(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(),
		s3types.ObjectLockRetentionMode(plan.Mode.ValueString()), retainUntil, bypass)
}

func (r *objectRetentionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectRetentionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.apply(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка применения retention объекта", err.Error())
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectRetentionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectRetentionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	retention, err := pcs3.GetObjectRetention(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString())
	if err != nil {
		if isNotFound(err) || isNoSuchObjectLockConfiguration(err) {
			// NoSuchObjectLockConfiguration ("The specified object does not have
			// a ObjectLock configuration") — код 400, не 404, поэтому isNotFound()
			// его не ловит; это object-level аналог ObjectLockConfigurationNotFoundError
			// у bucket-level Object Lock. Означает "retention'а больше нет" (снят
			// мимо Terraform, объект пересоздан заново без retention и т.п.) —
			// это дрифт, который надо отразить убиранием ресурса из state, а не
			// падением всего plan/destroy.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения retention объекта", err.Error())
		return
	}
	if retention == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Mode = types.StringValue(string(retention.Mode))
	if retention.RetainUntilDate != nil {
		state.RetainUntil = types.StringValue(retention.RetainUntilDate.Format(retentionTimeLayout))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *objectRetentionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan objectRetentionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Ослабление COMPLIANCE или сокращение GOVERNANCE без bypass PlatformCraft,
	// скорее всего, отклонит на уровне API (409 InvalidRequest / AccessDenied) —
	// намеренно не перехватываем это заранее, пользователь должен явно увидеть
	// отказ API, а не тихо разошедшееся состояние.
	if err := r.apply(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения retention объекта", err.Error())
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectRetentionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectRetentionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.Mode.ValueString() == string(s3types.ObjectLockRetentionModeCompliance) {
		// В отличие от GOVERNANCE, COMPLIANCE нельзя снять/сократить досрочно вообще
		// никаким флагом — это заложено в саму семантику режима, а не ограничение
		// провайдера. Пытаться всё равно бессмысленно: заведомо получим отказ API.
		resp.Diagnostics.AddWarning(
			"Retention в режиме COMPLIANCE не может быть снят досрочно",
			"platformcraft_object_retention удалён из state Terraform, но фактический retention объекта "+
				state.Bucket.ValueString()+"/"+state.Key.ValueString()+" остаётся в силе до "+state.RetainUntil.ValueString()+
				" — ни AWS, ни PlatformCraft не позволяют снять COMPLIANCE-режим досрочно никому, включая владельца аккаунта.",
		)
		return
	}

	// GOVERNANCE: пытаемся реально снять retention через bypass=true — в отличие
	// от Object Lock на бакете в целом, здесь это настоящее, а не бутафорское
	// снятие ограничения. На AWS для этого принято ставить retain_until в
	// прошлое — но PlatformCraft это отклоняет валидацией ("the retain until
	// date must be in the future") независимо от bypass. Поэтому ставим не
	// прошлую, а ближайшую будущую дату (с запасом на рассинхрон часов между
	// этой машиной и сервером PlatformCraft) — retention фактически перестаёт
	// действовать через эти секунды, что для целей Delete() практически то же
	// самое, что мгновенное снятие: сам вызов PutObjectRetention успешен прямо
	// сейчас, ресурс убирается из state сразу, а не через minute-ожидание.
	const releaseGrace = 60 * time.Second
	if err := pcs3.PutObjectRetention(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString(),
		s3types.ObjectLockRetentionModeGovernance, time.Now().Add(releaseGrace), true); err != nil {
		resp.Diagnostics.AddError("Ошибка снятия retention объекта при удалении ресурса", err.Error())
		return
	}
}

func (r *objectRetentionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_object_retention.example <bucket>,<key>
	parts := splitTwo(req.ID)
	if parts == nil {
		resp.Diagnostics.AddError(
			"Некорректный формат ID для импорта",
			`Ожидается "<bucket>,<key>", например: terraform import platformcraft_object_retention.example my-bucket,path/to/file.txt`,
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[0]+"/"+parts[1])...)
}

// isNoSuchObjectLockConfiguration — у объекта нет retention/legal hold.
// PlatformCraft отвечает на GetObjectRetention/GetObjectLegalHold кодом
// NoSuchObjectLockConfiguration со статусом 400, поэтому isNotFound его не ловит.
func isNoSuchObjectLockConfiguration(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchObjectLockConfiguration"
	}
	return false
}
