package provider

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

var (
	_ resource.Resource                = &bucketCorsResource{}
	_ resource.ResourceWithConfigure   = &bucketCorsResource{}
	_ resource.ResourceWithImportState = &bucketCorsResource{}
)

func NewBucketCorsResource() resource.Resource {
	return &bucketCorsResource{}
}

type bucketCorsResource struct {
	client *s3.Client
}

// CORS-конфигурация бакета: rule — повторяемый блок, 0..N правил. Порядок
// сохраняется и значим (S3 применяет первое подошедшее правило).
type bucketCorsResourceModel struct {
	Bucket types.String    `tfsdk:"bucket"`
	Rule   []corsRuleModel `tfsdk:"rule"`
	Id     types.String    `tfsdk:"id"`
}

// defaultCorsMaxAgeSeconds — значение max_age_seconds, если оно не задано в правиле.
const defaultCorsMaxAgeSeconds = 3000

type corsRuleModel struct {
	AllowedOrigins types.List  `tfsdk:"allowed_origins"`
	AllowedMethods types.List  `tfsdk:"allowed_methods"`
	AllowedHeaders types.List  `tfsdk:"allowed_headers"`
	MaxAgeSeconds  types.Int32 `tfsdk:"max_age_seconds"`
}

func (r *bucketCorsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_cors"
}

func (r *bucketCorsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет CORS-конфигурацией бакета PlatformCraft (put-bucket-cors/get-bucket-cors/delete-bucket-cors). Поддерживает несколько правил через повторяемый блок rule; порядок правил значим — как и в самом S3, применяется первое подошедшее.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета, для которого настраивается CORS.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
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
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "Одно CORS-правило. Можно указать несколько блоков rule — они применяются к бакету в том же порядке, в котором заданы в .tf.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"allowed_origins": schema.ListAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Список разрешённых origin, например [\"*\"] или [\"https://example.com\"].",
						},
						"allowed_methods": schema.ListAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Список разрешённых HTTP-методов, например [\"GET\", \"PUT\"].",
						},
						"allowed_headers": schema.ListAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Список разрешённых заголовков, например [\"*\"].",
						},
						"max_age_seconds": schema.Int32Attribute{
							Optional:    true,
							Computed:    true,
							Default:     int32default.StaticInt32(defaultCorsMaxAgeSeconds),
							Description: "Сколько секунд браузер кэширует результат preflight-запроса. По умолчанию 3000.",
						},
					},
				},
			},
		},
	}
}

func (r *bucketCorsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *bucketCorsResource) applyCors(ctx context.Context, plan bucketCorsResourceModel) error {
	rules := make([]pcs3.CorsRule, 0, len(plan.Rule))
	for _, ruleModel := range plan.Rule {
		var origins, methods, headers []string
		ruleModel.AllowedOrigins.ElementsAs(ctx, &origins, false)
		ruleModel.AllowedMethods.ElementsAs(ctx, &methods, false)
		ruleModel.AllowedHeaders.ElementsAs(ctx, &headers, false)

		maxAge := int32(defaultCorsMaxAgeSeconds)
		if !ruleModel.MaxAgeSeconds.IsNull() && !ruleModel.MaxAgeSeconds.IsUnknown() {
			maxAge = ruleModel.MaxAgeSeconds.ValueInt32()
		}

		rules = append(rules, pcs3.CorsRule{
			AllowedOrigins: origins,
			AllowedMethods: methods,
			AllowedHeaders: headers,
			MaxAgeSeconds:  maxAge,
		})
	}

	return pcs3.SetBucketCors(ctx, r.client, plan.Bucket.ValueString(), rules)
}

func (r *bucketCorsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketCorsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyCors(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка применения CORS-конфигурации", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketCorsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketCorsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rules, err := pcs3.GetBucketCors(ctx, r.client, state.Bucket.ValueString())
	if err != nil {
		if isNotFound(err) || isNoSuchCorsConfiguration(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения CORS-конфигурации", err.Error())
		return
	}
	if len(rules) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}

	// В state сохраняются все правила, которые фактически заданы на бакете.
	ruleModels := make([]corsRuleModel, 0, len(rules))
	for _, rule := range rules {
		origins, diags := types.ListValueFrom(ctx, types.StringType, rule.AllowedOrigins)
		resp.Diagnostics.Append(diags...)
		methods, diags := types.ListValueFrom(ctx, types.StringType, rule.AllowedMethods)
		resp.Diagnostics.Append(diags...)
		headers, diags := types.ListValueFrom(ctx, types.StringType, rule.AllowedHeaders)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		rm := corsRuleModel{
			AllowedOrigins: origins,
			AllowedMethods: methods,
			AllowedHeaders: headers,
		}
		if rule.MaxAgeSeconds != nil {
			rm.MaxAgeSeconds = types.Int32Value(*rule.MaxAgeSeconds)
		}
		ruleModels = append(ruleModels, rm)
	}

	state.Rule = ruleModels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bucketCorsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bucketCorsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.applyCors(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения CORS-конфигурации", err.Error())
		return
	}

	plan.Id = plan.Bucket
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *bucketCorsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketCorsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.DeleteBucketCors(ctx, r.client, state.Bucket.ValueString()); err != nil {
		if isNoSuchCorsConfiguration(err) {
			return
		}
		resp.Diagnostics.AddError("Ошибка удаления CORS-конфигурации", err.Error())
		return
	}
}

func (r *bucketCorsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_bucket_cors.example <имя-бакета>
	resource.ImportStatePassthroughID(ctx, path.Root("bucket"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func isNoSuchCorsConfiguration(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		// «CORS не настроен»: PlatformCraft использует код NoSuchBucketCors,
		// Amazon S3 — NoSuchCORSConfiguration; проверяем оба.
		switch apiErr.ErrorCode() {
		case "NoSuchCORSConfiguration", "NoSuchBucketCors":
			return true
		}
	}
	return false
}
