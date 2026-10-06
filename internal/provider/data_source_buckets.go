package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_buckets — имена всех бакетов аккаунта (list-buckets), например
// для for_each по существующим бакетам.

var (
	_ datasource.DataSource              = &bucketsDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketsDataSource{}
)

func NewBucketsDataSource() datasource.DataSource {
	return &bucketsDataSource{}
}

type bucketsDataSource struct {
	client *s3.Client
}

type bucketsDataSourceModel struct {
	Id      types.String `tfsdk:"id"`
	Buckets types.List   `tfsdk:"buckets"`
}

func (d *bucketsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_buckets"
}

func (d *bucketsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Список имён всех бакетов, доступных используемым ключам (list-buckets).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Статичный идентификатор в Terraform state.",
			},
			"buckets": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Имена всех доступных бакетов.",
			},
		},
	}
}

func (d *bucketsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketsDataSourceModel

	names, err := pcs3.ListBuckets(ctx, d.client)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения списка бакетов", err.Error())
		return
	}

	bucketsList, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue("platformcraft_buckets")
	data.Buckets = bucketsList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
