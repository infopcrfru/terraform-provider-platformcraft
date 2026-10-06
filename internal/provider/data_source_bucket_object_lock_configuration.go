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
	_ datasource.DataSource              = &bucketObjectLockConfigurationDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketObjectLockConfigurationDataSource{}
)

func NewBucketObjectLockConfigurationDataSource() datasource.DataSource {
	return &bucketObjectLockConfigurationDataSource{}
}

type bucketObjectLockConfigurationDataSource struct {
	client *s3.Client
}

type bucketObjectLockConfigurationDataSourceModel struct {
	Bucket types.String `tfsdk:"bucket"`
	Id     types.String `tfsdk:"id"`
	Mode   types.String `tfsdk:"mode"`
	Days   types.Int32  `tfsdk:"days"`
}

func (d *bucketObjectLockConfigurationDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_object_lock_configuration"
}

func (d *bucketObjectLockConfigurationDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает конфигурацию Object Lock по умолчанию существующего бакета PlatformCraft. Если Object Lock на бакете не включён, чтение завершается ошибкой.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
			"mode": schema.StringAttribute{
				Computed:    true,
				Description: "GOVERNANCE или COMPLIANCE.",
			},
			"days": schema.Int32Attribute{
				Computed:    true,
				Description: "Срок хранения по умолчанию в днях.",
			},
		},
	}
}

func (d *bucketObjectLockConfigurationDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketObjectLockConfigurationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketObjectLockConfigurationDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := pcs3.GetObjectLockConfiguration(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения Object Lock конфигурации", err.Error())
		return
	}
	if cfg == nil || cfg.Rule == nil || cfg.Rule.DefaultRetention == nil {
		resp.Diagnostics.AddError(
			"У бакета нет конфигурации Object Lock по умолчанию",
			"GetObjectLockConfiguration вернул пустое правило retention для бакета "+data.Bucket.ValueString()+".",
		)
		return
	}

	data.Id = data.Bucket
	data.Mode = types.StringValue(string(cfg.Rule.DefaultRetention.Mode))
	if cfg.Rule.DefaultRetention.Days != nil {
		data.Days = types.Int32Value(*cfg.Rule.DefaultRetention.Days)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
