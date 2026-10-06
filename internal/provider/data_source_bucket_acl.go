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

var (
	_ datasource.DataSource              = &bucketAclDataSource{}
	_ datasource.DataSourceWithConfigure = &bucketAclDataSource{}
)

func NewBucketAclDataSource() datasource.DataSource {
	return &bucketAclDataSource{}
}

type bucketAclDataSource struct {
	client *s3.Client
}

// В отличие от platformcraft_bucket_acl (resource), тут нет попытки угадать
// canned-строку ("private"/"public-read"/...) обратно из грантов — S3 API не
// хранит, каким именно canned ACL был выставлен грант, только фактический
// результат, и несколько разных canned ACL могут дать одинаковый набор
// грантов. Поэтому data source отдаёт то, что реально можно узнать честно:
// владельца и сырой список грантов.
type bucketAclDataSourceModel struct {
	Bucket           types.String    `tfsdk:"bucket"`
	Id               types.String    `tfsdk:"id"`
	OwnerId          types.String    `tfsdk:"owner_id"`
	OwnerDisplayName types.String    `tfsdk:"owner_display_name"`
	Grant            []aclGrantModel `tfsdk:"grant"`
}

type aclGrantModel struct {
	GranteeType        types.String `tfsdk:"grantee_type"`
	GranteeId          types.String `tfsdk:"grantee_id"`
	GranteeDisplayName types.String `tfsdk:"grantee_display_name"`
	GranteeUri         types.String `tfsdk:"grantee_uri"`
	Permission         types.String `tfsdk:"permission"`
}

func (d *bucketAclDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket_acl"
}

func (d *bucketAclDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает владельца и ACL-гранты существующего бакета. Canned ACL (private, public-read и т.д.) не восстанавливается: API возвращает только фактические гранты.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор в Terraform state (совпадает с bucket).",
			},
			"owner_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID владельца бакета.",
			},
			"owner_display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Отображаемое имя владельца бакета.",
			},
		},
		Blocks: map[string]schema.Block{
			"grant": schema.ListNestedBlock{
				Description: "Список ACL-грантов, как их вернул GetBucketAcl.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"grantee_type": schema.StringAttribute{
							Computed:    true,
							Description: "Тип грантополучателя: CanonicalUser, Group или AmazonCustomerByEmail.",
						},
						"grantee_id": schema.StringAttribute{
							Computed: true,
						},
						"grantee_display_name": schema.StringAttribute{
							Computed: true,
						},
						"grantee_uri": schema.StringAttribute{
							Computed:    true,
							Description: "URI группы для грантов типа Group, например AllUsers.",
						},
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

func (d *bucketAclDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *bucketAclDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data bucketAclDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	owner, grants, err := pcs3.GetBucketAcl(ctx, d.client, data.Bucket.ValueString())
	if err != nil {
		if isUnsupportedAclConfiguration(err) {
			resp.Diagnostics.AddError("ACL не поддерживается для этого бакета", unsupportedAclHint)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения ACL бакета", err.Error())
		return
	}

	data.Id = data.Bucket
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
