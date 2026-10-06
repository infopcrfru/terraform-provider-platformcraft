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

// platformcraft_object_versions — версии объектов и delete-маркеры бакета
// (list-object-versions). Имеет смысл при включённом версионировании.

var (
	_ datasource.DataSource              = &objectVersionsDataSource{}
	_ datasource.DataSourceWithConfigure = &objectVersionsDataSource{}
)

func NewObjectVersionsDataSource() datasource.DataSource {
	return &objectVersionsDataSource{}
}

type objectVersionsDataSource struct {
	client *s3.Client
}

type objectVersionsDataSourceModel struct {
	Bucket       types.String             `tfsdk:"bucket"`
	Id           types.String             `tfsdk:"id"`
	Version      []objectVersionItemModel `tfsdk:"version"`
	DeleteMarker []deleteMarkerItemModel  `tfsdk:"delete_marker"`
}

type objectVersionItemModel struct {
	Key          types.String `tfsdk:"key"`
	VersionId    types.String `tfsdk:"version_id"`
	IsLatest     types.Bool   `tfsdk:"is_latest"`
	Size         types.Int64  `tfsdk:"size"`
	Etag         types.String `tfsdk:"etag"`
	LastModified types.String `tfsdk:"last_modified"`
}

type deleteMarkerItemModel struct {
	Key          types.String `tfsdk:"key"`
	VersionId    types.String `tfsdk:"version_id"`
	IsLatest     types.Bool   `tfsdk:"is_latest"`
	LastModified types.String `tfsdk:"last_modified"`
}

func (d *objectVersionsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_versions"
}

func (d *objectVersionsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Версии объектов и delete-маркеры бакета (list-object-versions). Имеет смысл только при " +
			"включённом версионировании (platformcraft_bucket_versioning со status=\"Enabled\").",
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
			"version": schema.ListNestedBlock{
				Description: "Версии объектов.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"key":           schema.StringAttribute{Computed: true},
						"version_id":    schema.StringAttribute{Computed: true},
						"is_latest":     schema.BoolAttribute{Computed: true},
						"size":          schema.Int64Attribute{Computed: true},
						"etag":          schema.StringAttribute{Computed: true},
						"last_modified": schema.StringAttribute{Computed: true, Description: "RFC3339."},
					},
				},
			},
			"delete_marker": schema.ListNestedBlock{
				Description: "Delete-маркеры (следы удаления версионируемых объектов).",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"key":           schema.StringAttribute{Computed: true},
						"version_id":    schema.StringAttribute{Computed: true},
						"is_latest":     schema.BoolAttribute{Computed: true},
						"last_modified": schema.StringAttribute{Computed: true, Description: "RFC3339."},
					},
				},
			},
		},
	}
}

func (d *objectVersionsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *objectVersionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectVersionsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	versions, markers, err := pcs3.ListObjectVersions(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Ошибка получения версий объектов бакета", err.Error())
		return
	}

	versionItems := make([]objectVersionItemModel, 0, len(versions))
	for _, v := range versions {
		item := objectVersionItemModel{
			Key:       types.StringValue(aws.ToString(v.Key)),
			VersionId: types.StringValue(aws.ToString(v.VersionId)),
			Etag:      types.StringValue(aws.ToString(v.ETag)),
		}
		if v.IsLatest != nil {
			item.IsLatest = types.BoolValue(*v.IsLatest)
		}
		if v.Size != nil {
			item.Size = types.Int64Value(*v.Size)
		}
		if v.LastModified != nil {
			item.LastModified = types.StringValue(v.LastModified.Format("2006-01-02T15:04:05Z07:00"))
		}
		versionItems = append(versionItems, item)
	}

	markerItems := make([]deleteMarkerItemModel, 0, len(markers))
	for _, m := range markers {
		item := deleteMarkerItemModel{
			Key:       types.StringValue(aws.ToString(m.Key)),
			VersionId: types.StringValue(aws.ToString(m.VersionId)),
		}
		if m.IsLatest != nil {
			item.IsLatest = types.BoolValue(*m.IsLatest)
		}
		if m.LastModified != nil {
			item.LastModified = types.StringValue(m.LastModified.Format("2006-01-02T15:04:05Z07:00"))
		}
		markerItems = append(markerItems, item)
	}

	data.Id = data.Bucket
	data.Version = versionItems
	data.DeleteMarker = markerItems
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
