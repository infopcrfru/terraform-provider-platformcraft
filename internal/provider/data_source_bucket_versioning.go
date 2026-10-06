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
	_ datasource.DataSource              = &bucketVersioningDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketVersioningDataSource{}
)

func NewBucketVersioningDataSource() datasource.DataSource {
	return &bucketVersioningDataSource{}
}

type bucketVersioningDataSource struct {
	client *s3.Client
}

type bucketVersioningDataSourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Status types.String `tfsdk:"status"`
}

func (d *bucketVersioningDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_versioning"
}

func (d *bucketVersioningDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает текущий статус версионирования уже существующего бакета PlatformCraft.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Enabled, Suspended или Off (Off — версионирование никогда не включалось).",
			},
		},
	}
}

func (d *bucketVersioningDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketVersioningDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketVersioningDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := pcs3.GetBucketVersioning(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения статуса версионирования", err.Error())
		return
	}

	data.Id = data.Bucket
	data.Status = types.StringValue(status)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
