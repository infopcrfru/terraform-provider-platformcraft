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
	_ resource.Resource                = &bucketVersioningResource{}
	_ resource.ResourceWithConfigure   = &bucketVersioningResource{}
	_ resource.ResourceWithImportState = &bucketVersioningResource{}
)

func NewBucketVersioningResource() resource.Resource {
	return &bucketVersioningResource{}
}

type bucketVersioningResource struct {
	client *s3.Client
}

type bucketVersioningResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Status types.String `tfsdk:"status"`
	Id     types.String `tfsdk:"id"`
}

func (r *bucketVersioningResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_versioning"
}

func (r *bucketVersioningResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет версионированием бакета PlatformCraft (put-bucket-versioning/get-bucket-versioning).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета, для которого настраивается версионирование.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				Required:    true,
				Description: "Статус версионирования: Enabled или Suspended.",
				Validators: []validator.String{
					stringvalidator.OneOf("Enabled", "Suspended"),
				},
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

func (r *bucketVersioningResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketVersioningResource) applyVersioning(ctx context.Context, bucket, status string) error {
	return pcs3.SetBucketVersioning(ctx, r.client, bucket, s3types.BucketVersioningStatus(status))
}

func (r *bucketVersioningResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyVersioning(ctx, plan.Bucket.ValueString(), plan.Status.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка включения версионирования", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketVersioningResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := pcs3.GetBucketVersioning(ctx, r.client, state.Bucket.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения статуса версионирования", err.Error())
		return
	}

	state.Status = types.StringValue(status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketVersioningResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketVersioningResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Статус версионирования меняется на месте повторным put-bucket-versioning,
	// поэтому Update вызывает тот же путь, что и Create.
	if err := r.applyVersioning(ctx, plan.Bucket.ValueString(), plan.Status.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения статуса версионирования", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketVersioningResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketVersioningResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := state.Bucket.ValueString()

	err := r.applyVersioning(ctx, bucket, string(s3types.BucketVersioningStatusSuspended))
	if err == nil {
		return
	}

	if isInvalidBucketState(err) {
		// InvalidBucketState на попытке приостановить версионирование почти всегда
		// означает, что у бакета включён Object Lock: он требует, чтобы версионирование
		// оставалось Enabled навсегда — это тот же самый запрет S3, что не даёт выключить
		// сам Object Lock (см. Delete() в resource_bucket_object_lock_configuration.go),
		// просто он бьёт ещё и по приостановке версионирования. Проверяем предположение
		// через GetObjectLockConfiguration и, если оно подтвердилось, не превращаем это
		// в ошибку destroy — ресурс всё равно физически нельзя перевести в нужное
		// состояние ни при каких условиях.
		if cfg, lockErr := pcs3.GetObjectLockConfiguration(ctx, r.client, bucket); lockErr == nil &&
			cfg != nil && cfg.ObjectLockEnabled == s3types.ObjectLockEnabledEnabled {
			resp.Diagnostics.AddWarning(
				"Версионирование не приостановлено — бакет защищён Object Lock",
				"У бакета "+bucket+" включён Object Lock, а он требует, чтобы версионирование оставалось "+
					"включённым навсегда — приостановить его в этом состоянии нельзя ни через AWS, ни через "+
					"PlatformCraft API. platformcraft_bucket_versioning удалён из state Terraform, но физически "+
					"версионирование бакета остаётся Enabled. Это ожидаемо: то же ограничение S3 не позволяет "+
					"отключить и сам Object Lock.",
			)
			return
		}
	}

	resp.Diagnostics.AddError("Ошибка приостановки версионирования при удалении ресурса", err.Error())
}

func (r *bucketVersioningResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket_versioning.example <имя-бакета>
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func isInvalidBucketState(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "InvalidBucketState"
	}
	return false
}
