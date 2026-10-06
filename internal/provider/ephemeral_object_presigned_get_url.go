package provider

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_object_presigned_get_url — presigned URL на скачивание объекта.
// Это одноразовый секрет с ограниченным сроком действия, поэтому он реализован
// как ephemeral resource (Terraform 1.10+): значение не попадает ни в plan, ни
// в state и вычисляется только в той операции, где оно нужно.
//
// ВАЖНО ОБ ИСПОЛЬЗОВАНИИ: ephemeral-значения нельзя подставить в произвольный
// атрибут ресурса (Terraform это отклонит) — они годятся для того, что само умеет
// принимать ephemeral-данные: атрибуты provider-блоков, write-only атрибуты
// ресурсов (Terraform 1.11+), другие ephemeral resources и т.п.

var (
	_ ephemeral.EphemeralResource              = &objectPresignedGetUrlEphemeral{}
	_ ephemeral.EphemeralResourceWithConfigure = &objectPresignedGetUrlEphemeral{}
)

func NewObjectPresignedGetUrlEphemeralResource() ephemeral.EphemeralResource {
	return &objectPresignedGetUrlEphemeral{}
}

type objectPresignedGetUrlEphemeral struct {
	client *s3.Client
}

type objectPresignedUrlEphemeralModel struct {
	Bucket         types.String `tfsdk:"bucket"`
	Key            types.String `tfsdk:"key"`
	ExpiresSeconds types.Int64  `tfsdk:"expires_seconds"`
	Url            types.String `tfsdk:"url"`
}

func (e *objectPresignedGetUrlEphemeral) Metadata(ctx context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_presigned_get_url"
}

func (e *objectPresignedGetUrlEphemeral) Schema(ctx context.Context, req ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Предподписанная ссылка на скачивание объекта (presign get-object) как ephemeral-значение — " +
			"не сохраняется ни в plan, ни в state.",
		Attributes: map[string]ephschema.Attribute{
			"bucket": ephschema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"key": ephschema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта.",
			},
			"expires_seconds": ephschema.Int64Attribute{
				Optional:    true,
				Description: "Срок действия ссылки в секундах. По умолчанию 900 (15 минут).",
			},
			"url": ephschema.StringAttribute{
				Computed:    true,
				Description: "Предподписанный URL.",
			},
		},
	}
}

func (e *objectPresignedGetUrlEphemeral) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*s3.Client)
	if !ok {
		resp.Diagnostics.AddError("Неожиданный тип ProviderData", "Ожидался *s3.Client, это внутренняя ошибка провайдера.")
		return
	}
	e.client = client
}

func (e *objectPresignedGetUrlEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	if e.client == nil {
		resp.Diagnostics.AddError(
			"Клиент S3 не инициализирован",
			"Configure() не получил *s3.Client до вызова Open() — это внутренняя ошибка провайдера "+
				"(EphemeralResourceData не был передан из provider.Configure), а не ошибка вашей конфигурации.",
		)
		return
	}

	var data objectPresignedUrlEphemeralModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	expires := 900 * time.Second
	if !data.ExpiresSeconds.IsNull() && !data.ExpiresSeconds.IsUnknown() {
		expires = time.Duration(data.ExpiresSeconds.ValueInt64()) * time.Second
	}

	url, err := pcs3.PresignGetObject(ctx, e.client, data.Bucket.ValueString(), data.Key.ValueString(), expires)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка генерации presigned GET URL", err.Error())
		return
	}

	data.Url = types.StringValue(url)
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
