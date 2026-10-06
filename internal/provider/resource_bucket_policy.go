package provider

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

var (
	_ resource.Resource                = &bucketPolicyResource{}
	_ resource.ResourceWithConfigure   = &bucketPolicyResource{}
	_ resource.ResourceWithImportState = &bucketPolicyResource{}
)

func NewBucketPolicyResource() resource.Resource {
	return &bucketPolicyResource{}
}

type bucketPolicyResource struct {
	client *s3.Client
}

type bucketPolicyResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Policy types.String `tfsdk:"policy"`
	Id     types.String `tfsdk:"id"`
}

func (r *bucketPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_policy"
}

func (r *bucketPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет JSON-политикой бакета PlatformCraft (put-bucket-policy/get-bucket-policy/delete-bucket-policy). Используйте jsonencode(...) в .tf, чтобы не следить за экранированием вручную.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета, к которому применяется политика.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"policy": schema.StringAttribute{
				Required:    true,
				Description: "JSON-политика бакета.",
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

func (r *bucketPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.SetBucketPolicy(ctx, r.client, plan.Bucket.ValueString(), plan.Policy.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка применения политики бакета", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	remotePolicy, err := pcs3.GetBucketPolicy(ctx, r.client, state.Bucket.ValueString())
	if err != nil {
		if isNotFound(err) || isNoSuchBucketPolicy(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения политики бакета", err.Error())
		return
	}

	if remotePolicy == "" {
		// Политики нет (пустой документ после удаления мимо Terraform).
		resp.State.RemoveResource(ctx)
		return
	}

	// Сравниваем по смыслу (см. json_utils.go), а не побайтово: если PlatformCraft
	// вернул семантически ту же политику, но в другом форматировании — оставляем
	// в state то, что было (значение из .tf), чтобы не показывать ложный diff.
	// Если политика реально изменилась (кто-то поправил её мимо Terraform) — берём
	// то, что реально лежит на бакете, чтобы plan показал настоящий дрифт.
	if !jsonPolicyEqual(state.Policy.ValueString(), remotePolicy) {
		state.Policy = types.StringValue(remotePolicy)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.SetBucketPolicy(ctx, r.client, plan.Bucket.ValueString(), plan.Policy.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения политики бакета", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	removed, err := pcs3.DeleteBucketPolicyConfirmed(ctx, r.client, state.Bucket.ValueString(), 10*time.Second)
	if err != nil {
		if isNoSuchBucketPolicy(err) || isNotFound(err) {
			return // политики или бакета уже нет
		}
		resp.Diagnostics.AddError("Ошибка удаления политики бакета", err.Error())
		return
	}
	if !removed {
		resp.Diagnostics.AddWarning(
			"Политика бакета не удалена на стороне API",
			"API принял запрос delete-bucket-policy, но политика бакета "+state.Bucket.ValueString()+
				" продолжает читаться через get-bucket-policy (проверялось 10 секунд с повтором удаления). "+
				"Ресурс удалён из state Terraform. Проверьте политику в панели PlatformCraft.",
		)
	}
}

func (r *bucketPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket_policy.example <имя-бакета>
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// isNoSuchBucketPolicy — отдельно от isNotFound() в provider.go, потому что
// у "нет политики" свой код ошибки (NoSuchBucketPolicy), отличный от "нет бакета".
func isNoSuchBucketPolicy(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "NoSuchBucketPolicy"
	}
	return false
}
