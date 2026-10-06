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

// platformcraft_bucket_objects_v1 — список объектов через list-objects (v1).
// Отдельный data source, а не флаг у platformcraft_bucket_objects: ответы v1 и
// v2 устроены по-разному (в v1 владелец у каждого объекта, пагинация через
// Marker/NextMarker вместо ContinuationToken).
var (
	_ datasource.DataSource              = &bucketObjectsV1DataSource{}
	_ datasource.DataSourceWithConfigure = &bucketObjectsV1DataSource{}
)

func NewBucketObjectsV1DataSource() datasource.DataSource {
	return &bucketObjectsV1DataSource{}
}

type bucketObjectsV1DataSource struct {
	client *s3.Client
}

type bucketObjectsV1DataSourceModel struct {
	Bucket  types.String              `tfsdk:"bucket"`
	Id      types.String              `tfsdk:"id"`
	Objects []bucketObjectV1ItemModel `tfsdk:"objects"`
}

type bucketObjectV1ItemModel struct {
	Key          types.String `tfsdk:"key"`
	Size         types.Int64  `tfsdk:"size"`
	Etag         types.String `tfsdk:"etag"`
	LastModified types.String `tfsdk:"last_modified"`
	OwnerId      types.String `tfsdk:"owner_id"`
}

func (d *bucketObjectsV1DataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_objects_v1"
}

func (d *bucketObjectsV1DataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Список объектов бакета через list-objects (v1) с автоматической пагинацией. В отличие от platformcraft_bucket_objects, возвращает владельца каждого объекта. Выполняется полный листинг бакета без фильтра по префиксу.",
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
						"owner_id": schema.StringAttribute{
							Computed:    true,
							Description: "ID владельца объекта.",
						},
					},
				},
			},
		},
	}
}

func (d *bucketObjectsV1DataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketObjectsV1DataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketObjectsV1DataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	objs, err := pcs3.ListObjects(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения списка объектов бакета (v1)", err.Error())
		return
	}

	items := make([]bucketObjectV1ItemModel, 0, len(objs))
	for _, o := range objs {
		item := bucketObjectV1ItemModel{
			Key:  types.StringValue(aws.ToString(o.Key)),
			Etag: types.StringValue(aws.ToString(o.ETag)),
		}
		if o.Size != nil {
			item.Size = types.Int64Value(*o.Size)
		}
		if o.LastModified != nil {
			item.LastModified = types.StringValue(o.LastModified.Format("2006-01-02T15:04:05Z07:00"))
		}
		if o.Owner != nil {
			item.OwnerId = types.StringValue(aws.ToString(o.Owner.ID))
		}
		items = append(items, item)
	}

	data.Id = data.Bucket
	data.Objects = items
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
