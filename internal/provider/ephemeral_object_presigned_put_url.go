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

// platformcraft_object_presigned_put_url — то же самое, что presigned_get_url, но
// для загрузки. Модель (objectPresignedUrlEphemeralModel) переиспользуется из
// ephemeral_object_presigned_get_url.go — поля идентичны.

var (
	_ ephemeral.EphemeralResource              = &objectPresignedPutUrlEphemeral{}
	_ ephemeral.EphemeralResourceWithConfigure = &objectPresignedPutUrlEphemeral{}
)

func NewObjectPresignedPutUrlEphemeralResource() ephemeral.EphemeralResource {
	return &objectPresignedPutUrlEphemeral{}
}

type objectPresignedPutUrlEphemeral struct {
	client *s3.Client
}

func (e *objectPresignedPutUrlEphemeral) Metadata(ctx context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_presigned_put_url"
}

func (e *objectPresignedPutUrlEphemeral) Schema(ctx context.Context, req ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Предподписанная ссылка на загрузку объекта (presign put-object) как ephemeral-значение — " +
			"не сохраняется ни в plan, ни в state.",
		Attributes: map[string]ephschema.Attribute{
			"bucket": ephschema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
			},
			"key": ephschema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта, который будет загружен по этой ссылке.",
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

func (e *objectPresignedPutUrlEphemeral) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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

func (e *objectPresignedPutUrlEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
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

	url, err := pcs3.PresignPutObject(ctx, e.client, data.Bucket.ValueString(), data.Key.ValueString(), expires)
	if err != nil {
		resp.Diagnostics.AddError("Ошибка генерации presigned PUT URL", err.Error())
		return
	}

	data.Url = types.StringValue(url)
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
