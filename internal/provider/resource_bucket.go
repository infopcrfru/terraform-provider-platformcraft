package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	stringvalidator "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

var (
	_ resource.Resource                = &bucketResource{}
	_ resource.ResourceWithConfigure   = &bucketResource{}
	_ resource.ResourceWithImportState = &bucketResource{}
)

func NewBucketResource() resource.Resource {
	return &bucketResource{}
}

type bucketResource struct {
	client *s3.Client
}

type bucketResourceModel struct {
	Bucket       types.String `tfsdk:"bucket"`
	ForceDestroy types.Bool   `tfsdk:"force_destroy"`
	Id           types.String `tfsdk:"id"`
}

func (r *bucketResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (r *bucketResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Бакет в PlatformCraft S3 Object Storage (create-bucket/head-bucket/delete-bucket).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета. Уникально во всей системе PlatformCraft. Переименование невозможно — смена значения пересоздаёт ресурс.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					// Пустое имя (например, незаданная переменная в PowerShell) отсекается
					// на plan: API на CreateBucket с пустым именем отвечает 405 MethodNotAllowed,
					// по которому причину не понять.
					stringvalidator.LengthAtLeast(1),
				},
			},
			"force_destroy": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Если true, при удалении ресурса из бакета сначала удаляются все объекты, их версии и delete-маркеры (с обходом GOVERNANCE и снятием legal hold), затем сам бакет. Без этого удаление непустого бакета завершается ошибкой BucketNotEmpty. Версии под retention в режиме COMPLIANCE удалить нельзя. По умолчанию false. Значение хранится только в state Terraform.",
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

func (r *bucketResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.CreateBucket(ctx, r.client, plan.Bucket.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка создания бакета", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.HeadBucket(ctx, r.client, state.Bucket.ValueString()); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка проверки бакета", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// bucket помечен RequiresReplace, поэтому сюда попадаем только при смене
	// force_destroy — это настройка самого Terraform, в API ничего не пишется.
	var plan bucketResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.ForceDestroy.ValueBool() {
		if err := pcs3.EmptyBucket(ctx, r.client, state.Bucket.ValueString()); err != nil {
			if isNotFound(err) {
				return // бакет уже удалён мимо Terraform
			}
			resp.Diagnostics.AddError("Ошибка очистки бакета перед удалением (force_destroy)", err.Error())
			return
		}
	}

	if err := pcs3.DeleteBucket(ctx, r.client, state.Bucket.ValueString()); err != nil {
		if isNotFound(err) {
			return // бакет уже удалён мимо Terraform
		}
		resp.Diagnostics.AddError("Ошибка удаления бакета", err.Error())
		return
	}
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket.example <имя-бакета>
	// id заполняется сразу: иначе UseStateForUnknown удержит его null, и первый
	// apply закончится "Provider produced inconsistent result after apply".
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("force_destroy"), false)...)
}
