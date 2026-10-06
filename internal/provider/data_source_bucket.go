package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// Data source platformcraft_bucket: чтение параметров уже существующего бакета,
// созданного вручную или в другой конфигурации Terraform.

var (
	_ datasource.DataSource              = &bucketDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketDataSource{}
)

func NewBucketDataSource() datasource.DataSource {
	return &bucketDataSource{}
}

type bucketDataSource struct {
	client *s3.Client
}

type bucketDataSourceModel struct {
	Bucket           types.String `tfsdk:"bucket"`
	Id               types.String `tfsdk:"id"`
	VersioningStatus types.String `tfsdk:"versioning_status"`
	ObjectCount      types.Int64  `tfsdk:"object_count"`
	TotalSizeBytes   types.Int64  `tfsdk:"total_size_bytes"`
}

func (d *bucketDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (d *bucketDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает параметры уже существующего бакета PlatformCraft (не обязательно созданного этим провайдером).",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
			"versioning_status": schema.StringAttribute{
				Computed:    true,
				Description: "Текущий статус версионирования: Enabled, Suspended или Off.",
			},
			"object_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Количество объектов в бакете на момент чтения. Считается полным листингом бакета — на бакетах с очень большим числом объектов это может быть медленно.",
			},
			"total_size_bytes": schema.Int64Attribute{
				Computed:    true,
				Description: "Суммарный размер всех объектов в бакете в байтах.",
			},
		},
	}
}

func (d *bucketDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := data.Bucket.ValueString()

	if err := pcs3.HeadBucket(ctx, d.client, bucket); err != nil {
		resp.Diagnostics.AddError("Бакет не найден или недоступен", err.Error())
		return
	}

	versioningStatus, err := pcs3.GetBucketVersioning(ctx, d.client, bucket)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения статуса версионирования", err.Error())
		return
	}

	count, size, err := pcs3.GetBucketStats(ctx, d.client, bucket)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка подсчёта статистики бакета", err.Error())
		return
	}

	data.Id = data.Bucket
	data.VersioningStatus = types.StringValue(versioningStatus)
	data.ObjectCount = types.Int64Value(count)
	data.TotalSizeBytes = types.Int64Value(size)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
