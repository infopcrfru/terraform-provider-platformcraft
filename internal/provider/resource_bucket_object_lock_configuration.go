package provider

import (
	"context"
	"errors"

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

var (
	_ resource.Resource                = &bucketObjectLockConfigurationResource{}
	_ resource.ResourceWithConfigure   = &bucketObjectLockConfigurationResource{}
	_ resource.ResourceWithImportState = &bucketObjectLockConfigurationResource{}
)

func NewBucketObjectLockConfigurationResource() resource.Resource {
	return &bucketObjectLockConfigurationResource{}
}

type bucketObjectLockConfigurationResource struct {
	client *s3.Client
}

type bucketObjectLockConfigurationResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Mode   types.String `tfsdk:"mode"`
	Days   types.Int32  `tfsdk:"days"`
	Id     types.String `tfsdk:"id"`
}

func (r *bucketObjectLockConfigurationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_object_lock_configuration"
}

func (r *bucketObjectLockConfigurationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет конфигурацией Object Lock бакета (put/get-object-lock-configuration) — правилом retention по умолчанию для новых объектов. Требует включённого версионирования: если оно задаётся ресурсом platformcraft_bucket_versioning, укажите его в depends_on. Режим COMPLIANCE нельзя ослабить или снять до истечения срока никому, включая владельца аккаунта. Отключить Object Lock на бакете нельзя: при удалении ресурса конфигурация остаётся на бакете, а ресурс удаляется из state с предупреждением.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"mode": schema.StringAttribute{
				Required:    true,
				Description: "Режим retention по умолчанию: GOVERNANCE или COMPLIANCE.",
				Validators: []validator.String{
					stringvalidator.OneOf("GOVERNANCE", "COMPLIANCE"),
				},
			},
			"days": schema.Int32Attribute{
				Required:    true,
				Description: "Срок хранения по умолчанию в днях для новых объектов бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор ресурса в Terraform state (совпадает с bucket).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *bucketObjectLockConfigurationResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// checkVersioningEnabled даёт понятную ошибку вместо сырого ответа API, если
// версионирование бакета не включено — самая частая причина падения этого ресурса.
func (r *bucketObjectLockConfigurationResource) checkVersioningEnabled(ctx context.Context, bucket string) error {
	status, err := pcs3.GetBucketVersioning(ctx, r.client, bucket)
	if err != nil {
		return err
	}
	if status != string(s3types.BucketVersioningStatusEnabled) {
		return errors.New("версионирование бакета должно быть включено (status=Enabled) до применения Object Lock конфигурации; сейчас: " + status)
	}
	return nil
}

func (r *bucketObjectLockConfigurationResource) apply(ctx context.Context, plan bucketObjectLockConfigurationResourceModel) error {
	return pcs3.PutObjectLockConfiguration(ctx, r.client, plan.Bucket.ValueString(), s3types.ObjectLockRetentionMode(plan.Mode.ValueString()), plan.Days.ValueInt32())
}

func (r *bucketObjectLockConfigurationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketObjectLockConfigurationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.checkVersioningEnabled(ctx, plan.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Версионирование бакета не включено", err.Error())
		return
	}

	if err := r.apply(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка применения Object Lock конфигурации", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketObjectLockConfigurationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketObjectLockConfigurationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := pcs3.GetObjectLockConfiguration(ctx, r.client, state.Bucket.ValueString())
	if err != nil {
		if isNotFound(err) || isObjectLockConfigurationNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения Object Lock конфигурации", err.Error())
		return
	}
	if cfg == nil || cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.Mode = types.StringValue(string(cfg.Rule.DefaultRetention.Mode))
	if cfg.Rule.DefaultRetention.Days != nil {
		state.Days = types.Int32Value(*cfg.Rule.DefaultRetention.Days)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketObjectLockConfigurationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketObjectLockConfigurationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Ослабление COMPLIANCE -> GOVERNANCE API, скорее всего, отклонит. Ошибка
	// намеренно не перехватывается: пользователь должен увидеть отказ API.
	if err := r.apply(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения Object Lock конфигурации", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketObjectLockConfigurationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// У S3-совместимого API (ни у AWS, ни у PlatformCraft) нет метода "выключить
	// Object Lock" — put-object-lock-configuration всегда требует ObjectLockEnabled,
	// отключить его после включения нельзя в принципе. Поэтому Delete этого ресурса
	// физически ничего не удаляет на стороне API — только убирает ресурс из state
	// Terraform, с явным предупреждением.
	resp.Diagnostics.AddWarning(
		"Object Lock конфигурация не может быть отключена через API",
		"platformcraft_bucket_object_lock_configuration удалён из state, но правило retention по умолчанию физически остаётся на бакете — ни AWS, ни PlatformCraft не поддерживают отключение Object Lock после включения.",
	)
}

func (r *bucketObjectLockConfigurationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket_object_lock_configuration.example <имя-бакета>
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func isObjectLockConfigurationNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ObjectLockConfigurationNotFoundError"
	}
	return false
}
