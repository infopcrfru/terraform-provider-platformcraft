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
	_ datasource.DataSource              = &bucketPolicyDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketPolicyDataSource{}
)

func NewBucketPolicyDataSource() datasource.DataSource {
	return &bucketPolicyDataSource{}
}

type bucketPolicyDataSource struct {
	client *s3.Client
}

type bucketPolicyDataSourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Policy types.String `tfsdk:"policy"`
}

func (d *bucketPolicyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_policy"
}

func (d *bucketPolicyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает JSON-политику существующего бакета PlatformCraft. Если у бакета нет политики, policy — пустая строка.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
			"policy": schema.StringAttribute{
				Computed:    true,
				Description: "JSON-политика бакета как строка; пустая строка, если политики нет.",
			},
		},
	}
}

func (d *bucketPolicyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketPolicyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketPolicyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Бакет без политики — policy = "" (и для NoSuchBucketPolicy, и для пустого
	// документа, который PlatformCraft отдаёт после удаления политики).
	policy, err := pcs3.GetBucketPolicy(ctx, d.client, data.Bucket.ValueString())
	if err != nil && !isNoSuchBucketPolicy(err) {
		resp.Diagnostics.AddError("Ошибка получения политики бакета", err.Error())
		return
	}

	data.Id = data.Bucket
	data.Policy = types.StringValue(policy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
