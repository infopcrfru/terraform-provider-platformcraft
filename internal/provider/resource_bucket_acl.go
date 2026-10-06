package provider

import (
	"context"

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
	_ resource.Resource                = &bucketAclResource{}
	_ resource.ResourceWithConfigure   = &bucketAclResource{}
	_ resource.ResourceWithImportState = &bucketAclResource{}
)

func NewBucketAclResource() resource.Resource {
	return &bucketAclResource{}
}

type bucketAclResource struct {
	client *s3.Client
}

type bucketAclResourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Acl    types.String `tfsdk:"acl"`
	Id     types.String `tfsdk:"id"`
}

func (r *bucketAclResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_acl"
}

func (r *bucketAclResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет canned ACL бакета PlatformCraft (put-bucket-acl/get-bucket-acl).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета, для которого настраивается ACL.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"acl": schema.StringAttribute{
				Required:    true,
				Description: "Canned ACL: private, public-read, public-read-write или authenticated-read.",
				Validators: []validator.String{
					stringvalidator.OneOf("private", "public-read", "public-read-write", "authenticated-read"),
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

func (r *bucketAclResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketAclResource) applyAcl(ctx context.Context, bucket, acl string) error {
	return pcs3.SetBucketAcl(ctx, r.client, bucket, s3types.BucketCannedACL(acl))
}

func (r *bucketAclResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketAclResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyAcl(ctx, plan.Bucket.ValueString(), plan.Acl.ValueString()); err != nil {
		if isUnsupportedAclConfiguration(err) {
			resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
			return
		}
		resp.Diagnostics.AddError("Ошибка установки ACL бакета", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketAclResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketAclResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// ВАЖНО: GetBucketAcl отдаёт список грантов (Grantee+Permission), а не canned-строку
	// вроде "private"/"public-read" — S3-совместимое API не хранит, каким именно canned
	// ACL был выставлен грант, только его фактический результат. Однозначно и надёжно
	// восстановить "private"/"public-read"/... обратно из грантов нельзя (несколько
	// разных canned ACL могут дать одинаковый набор грантов, и наоборот — грант мог
	// быть выставлен вручную мимо Terraform). Поэтому здесь только проверяем, что
	// бакет существует и ACL-запрос вообще отрабатывает (значит, ресурс "жив"),
	// а само значение acl оставляем как в state — это осознанный компромисс,
	// а не недоработка. Фактические гранты отдают data sources
	// platformcraft_bucket_acl/platformcraft_object_acl.
	if _, _, err := pcs3.GetBucketAcl(ctx, r.client, state.Bucket.ValueString()); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		if isUnsupportedAclConfiguration(err) {
			resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения ACL бакета", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketAclResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketAclResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyAcl(ctx, plan.Bucket.ValueString(), plan.Acl.ValueString()); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения ACL бакета", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketAclResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// У S3 нет отдельного "удалить ACL" — есть только "поставить другой ACL".
	// При удалении ресурса возвращаем бакет к безопасному значению по умолчанию (private),
	// а не оставляем последнее применённое значение висеть бесхозным.
	var state bucketAclResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyAcl(ctx, state.Bucket.ValueString(), string(s3types.BucketCannedACLPrivate)); err != nil {
		resp.Diagnostics.AddError("Ошибка сброса ACL бакета в private при удалении ресурса", err.Error())
		return
	}
}

func (r *bucketAclResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket_acl.example <имя-бакета>
	// После импорта значение acl нужно прописать в .tf вручную — Read() не может
	// однозначно восстановить canned-строку из грантов (см. комментарий в Read).
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
