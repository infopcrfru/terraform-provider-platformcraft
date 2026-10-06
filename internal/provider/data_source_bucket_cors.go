package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

var (
	_ datasource.DataSource              = &bucketCorsDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketCorsDataSource{}
)

func NewBucketCorsDataSource() datasource.DataSource {
	return &bucketCorsDataSource{}
}

type bucketCorsDataSource struct {
	client *s3.Client
}

type bucketCorsDataSourceModel struct {
	Bucket types.String    `tfsdk:"bucket"`
	Id     types.String    `tfsdk:"id"`
	Rule   []corsRuleModel `tfsdk:"rule"`
}

func (d *bucketCorsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_cors"
}

func (d *bucketCorsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает все CORS-правила уже существующего бакета PlatformCraft.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
		},
		Blocks: map[string]schema.Block{
			"rule": schema.ListNestedBlock{
				Description: "Правила CORS, в порядке, в котором они лежат на бакете.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"allowed_origins": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
						"allowed_methods": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
						"allowed_headers": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
						"max_age_seconds": schema.Int32Attribute{
							Computed: true,
						},
					},
				},
			},
		},
	}
}

func (d *bucketCorsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*s3.Client)
	if !ok {
		resp.Diagnostics.AddError("Неожиданный тип ProviderData", "Ожидался *s3.Client, это внутренняя ошибка провайдера.")
		return
	}
	d.client = client
}

func (d *bucketCorsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketCorsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rules, err := pcs3.GetBucketCors(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения CORS-конфигурации", err.Error())
		return
	}

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

	data.Id = data.Bucket
	data.Rule = ruleModels
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
