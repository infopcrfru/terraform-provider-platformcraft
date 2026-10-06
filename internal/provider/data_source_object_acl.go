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

// platformcraft_object_acl — владелец и гранты ACL объекта (get-object-acl).
// Как и platformcraft_bucket_acl (data source), не пытается угадать canned-строку
// обратно из грантов — S3 API её не хранит, только фактический результат. Модель
// грантов (aclGrantModel) переиспользуется из data_source_bucket_acl.go.

var (
	_ datasource.DataSource              = &objectAclDataSource{}
	_ datasource.DataSourceWithConfigure = &objectAclDataSource{}
)

func NewObjectAclDataSource() datasource.DataSource {
	return &objectAclDataSource{}
}

type objectAclDataSource struct {
	client *s3.Client
}

type objectAclDataSourceModel struct {
	Bucket           types.String    `tfsdk:"bucket"`
	Key              types.String    `tfsdk:"key"`
	Id               types.String    `tfsdk:"id"`
	OwnerId          types.String    `tfsdk:"owner_id"`
	OwnerDisplayName types.String    `tfsdk:"owner_display_name"`
	Grant            []aclGrantModel `tfsdk:"grant"`
}

func (d *objectAclDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_acl"
}

func (d *objectAclDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает владельца и ACL-гранты объекта. Canned ACL (private, public-read и т.д.) не восстанавливается: API возвращает только фактические гранты.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"key": schema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (bucket/key).",
			},
			"owner_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID владельца объекта.",
			},
			"owner_display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Отображаемое имя владельца объекта.",
			},
		},
		Blocks: map[string]schema.Block{
			"grant": schema.ListNestedBlock{
				Description: "Список ACL-грантов, как их вернул GetObjectAcl.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"grantee_type": schema.StringAttribute{
							Computed:    true,
							Description: "Тип грантополучателя: CanonicalUser, Group или AmazonCustomerByEmail.",
						},
						"grantee_id":           schema.StringAttribute{Computed: true},
						"grantee_display_name": schema.StringAttribute{Computed: true},
						"grantee_uri":          schema.StringAttribute{Computed: true, Description: "URI группы для грантов типа Group, например AllUsers."},
						"permission": schema.StringAttribute{
							Computed:    true,
							Description: "READ, WRITE, READ_ACP, WRITE_ACP или FULL_CONTROL.",
						},
					},
				},
			},
		},
	}
}

func (d *objectAclDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *objectAclDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectAclDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	owner, grants, err := pcs3.GetObjectAcl(ctx, d.client, data.Bucket.ValueString(), data.Key.ValueString())
	if err != nil {
		if isUnsupportedAclConfiguration(err) {
			resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения ACL объекта", err.Error())
		return
	}

	data.Id = types.StringValue(data.Bucket.ValueString() + "/" + data.Key.ValueString())
	if owner != nil {
		data.OwnerId = types.StringValue(aws.ToString(owner.ID))
		data.OwnerDisplayName = types.StringValue(aws.ToString(owner.DisplayName))
	}

	grantModels := make([]aclGrantModel, 0, len(grants))
	for _, g := range grants {
		gm := aclGrantModel{
			Permission: types.StringValue(string(g.Permission)),
		}
		if g.Grantee != nil {
			gm.GranteeType = types.StringValue(string(g.Grantee.Type))
			gm.GranteeId = types.StringValue(aws.ToString(g.Grantee.ID))
			gm.GranteeDisplayName = types.StringValue(aws.ToString(g.Grantee.DisplayName))
			gm.GranteeUri = types.StringValue(aws.ToString(g.Grantee.URI))
		}
		grantModels = append(grantModels, gm)
	}
	data.Grant = grantModels

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
