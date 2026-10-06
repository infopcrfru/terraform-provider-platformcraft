package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_bucket_objects — список объектов бакета через list-objects-v2
// с пагинацией (например, для for_each по существующим объектам).

var (
	_ datasource.DataSource              = &bucketObjectsDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketObjectsDataSource{}
)

func NewBucketObjectsDataSource() datasource.DataSource {
	return &bucketObjectsDataSource{}
}

type bucketObjectsDataSource struct {
	client *s3.Client
}

type bucketObjectsDataSourceModel struct {
	Bucket  types.String            `tfsdk:"bucket"`
	Id      types.String            `tfsdk:"id"`
	Objects []bucketObjectItemModel `tfsdk:"objects"`
}

type bucketObjectItemModel struct {
	Key          types.String `tfsdk:"key"`
	Size         types.Int64  `tfsdk:"size"`
	Etag         types.String `tfsdk:"etag"`
	LastModified types.String `tfsdk:"last_modified"`
}

func (d *bucketObjectsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_objects"
}

func (d *bucketObjectsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Список объектов бакета (list-objects-v2) с автоматической пагинацией. Выполняется полный листинг бакета без фильтра по префиксу, поэтому на бакетах с большим числом объектов чтение может занять время.",
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
			"objects": schema.ListNestedBlock{
				Description: "Объекты бакета.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							Computed: true,
						},
						"size": schema.Int64Attribute{
							Computed: true,
						},
						"etag": schema.StringAttribute{
							Computed: true,
						},
						"last_modified": schema.StringAttribute{
							Computed:    true,
							Description: "RFC3339.",
						},
					},
				},
			},
		},
	}
}

func (d *bucketObjectsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketObjectsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketObjectsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	objs, err := pcs3.ListObjectsV2(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения списка объектов бакета", err.Error())
		return
	}

	items := make([]bucketObjectItemModel, 0, len(objs))
	for _, o := range objs {
		item := bucketObjectItemModel{
			Key:  types.StringValue(aws.ToString(o.Key)),
			Etag: types.StringValue(aws.ToString(o.ETag)),
		}
		if o.Size != nil {
			item.Size = types.Int64Value(*o.Size)
		}
		if o.LastModified != nil {
			item.LastModified = types.StringValue(o.LastModified.Format("2006-01-02T15:04:05Z07:00"))
		}
		items = append(items, item)
	}

	data.Id = data.Bucket
	data.Objects = items
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
