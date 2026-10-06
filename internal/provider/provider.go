package provider

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"github.com/aws/smithy-go/middleware"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// Проверка на этапе компиляции, что провайдер реализует нужные интерфейсы.
var _ provider.Provider = &platformcraftProvider{}
var _ provider.ProviderWithEphemeralResources = &platformcraftProvider{}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &platformcraftProvider{version: version}
	}
}

// version приходит из main.go (goreleaser выставляет её через
// -ldflags "-X main.version=...", при локальной сборке — "dev") и отдаётся в
// Metadata(): её видно в логах Terraform, это помогает при диагностике.
type platformcraftProvider struct {
	version string
}

// platformcraftProviderModel — поля блока `provider "platformcraft" {}`.
type platformcraftProviderModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Region    types.String `tfsdk:"region"`
	AccessKey types.String `tfsdk:"access_key"`
	SecretKey types.String `tfsdk:"secret_key"`
	MaxRPS    types.Int64  `tfsdk:"max_requests_per_second"`
}

func (p *platformcraftProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "platformcraft"
	resp.Version = p.version
}

func (p *platformcraftProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Провайдер для PlatformCraft S3 Object Storage (S3-совместимое API, эндпоинт https://eu-s3.platformcraft.com).",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Эндпоинт PlatformCraft S3. По умолчанию https://eu-s3.platformcraft.com. Также читается из PLATFORMCRAFT_ENDPOINT.",
			},
			"region": schema.StringAttribute{
				Optional:    true,
				Description: "Регион для подписи запросов (SigningRegion). По умолчанию eu-central-2. Также читается из PLATFORMCRAFT_REGION.",
			},
			"access_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "AWS Access Key ID, сгенерированный в личном кабинете PlatformCraft (Настройки → Доступы S3). Также читается из PLATFORMCRAFT_ACCESS_KEY или AWS_ACCESS_KEY_ID. Если не задан нигде — используется стандартная цепочка поиска credentials из AWS SDK (~/.aws/credentials и т.д.).",
			},
			"max_requests_per_second": schema.Int64Attribute{
				Optional: true,
				Description: "Максимум запросов к API в секунду от процесса провайдера (включая повторы). " +
					"Лимит PlatformCraft по умолчанию — 70 rps на клиента, при превышении API отвечает 429. " +
					"По умолчанию 50. 0 — без ограничения. Также читается из PLATFORMCRAFT_MAX_RPS.",
				Validators: []validator.Int64{int64validator.AtLeast(0)},
			},
			"secret_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "AWS Secret Access Key. Также читается из PLATFORMCRAFT_SECRET_KEY или AWS_SECRET_ACCESS_KEY.",
			},
		},
	}
}

func (p *platformcraftProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data platformcraftProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := stringOrEnv(data.Endpoint, "PLATFORMCRAFT_ENDPOINT", "https://eu-s3.platformcraft.com")
	region := stringOrEnv(data.Region, "PLATFORMCRAFT_REGION", "eu-central-2")

	accessKey := stringOrEnv(data.AccessKey, "PLATFORMCRAFT_ACCESS_KEY", "")
	secretKey := stringOrEnv(data.SecretKey, "PLATFORMCRAFT_SECRET_KEY", "")
	if (accessKey == "") != (secretKey == "") {
		// Задан только один из двух ключей — почти наверняка опечатка в имени
		// переменной окружения. Молча уйти в стандартную цепочку AWS SDK здесь
		// опасно: подхватятся чужие ключи из ~/.aws/credentials, и все запросы
		// будут отвечать 403 без понятной причины.
		resp.Diagnostics.AddError(
			"Задан только один из ключей доступа",
			"access_key и secret_key (или PLATFORMCRAFT_ACCESS_KEY и PLATFORMCRAFT_SECRET_KEY) должны быть заданы вместе. "+
				"Сейчас задан только один из них.",
		)
		return
	}

	maxRPS := pcs3.DefaultMaxRequestsPerSecond
	if !data.MaxRPS.IsNull() && !data.MaxRPS.IsUnknown() {
		maxRPS = int(data.MaxRPS.ValueInt64())
	} else if v := os.Getenv("PLATFORMCRAFT_MAX_RPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			resp.Diagnostics.AddError("Некорректное значение PLATFORMCRAFT_MAX_RPS",
				"Ожидается целое число >= 0 (0 — без ограничения), получено "+strconv.Quote(v)+".")
			return
		}
		maxRPS = n
	}

	client, err := newS3Client(ctx, endpoint, region, accessKey, secretKey, maxRPS)
	if err != nil {
		resp.Diagnostics.AddError("Не удалось собрать конфигурацию AWS SDK", err.Error())
		return
	}

	// Один клиент на все ресурсы, data sources и ephemeral resources.
	// EphemeralResourceData — отдельное поле протокола: без него Configure()
	// ephemeral-ресурсов получает nil.
	resp.ResourceData = client
	resp.DataSourceData = client
	resp.EphemeralResourceData = client
}

// newS3Client собирает *s3.Client для PlatformCraft. Вынесено из Configure,
// чтобы acceptance-тесты (подготовка дрифта, аварийная очистка бакетов)
// работали с API ровно через такой же клиент, как и сам провайдер.
//
// Если accessKey/secretKey пустые — используется стандартная цепочка поиска
// credentials AWS SDK (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, ~/.aws/credentials).
//
// maxRPS ограничивает частоту запросов (pcs3.RateLimitAPIOption), а
// ответы 429 повторяются (pcs3.NewRetryer).
func newS3Client(ctx context.Context, endpoint, region, accessKey, secretKey string, maxRPS int) (*s3.Client, error) {
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, signingRegion string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{URL: endpoint, SigningRegion: signingRegion}, nil
	})

	cfgOpts := []func(*config.LoadOptions) error{
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithRegion(region),
		config.WithAPIOptions([]func(*middleware.Stack) error{pcs3.RateLimitAPIOption(maxRPS)}),
		config.WithRetryer(pcs3.NewRetryer),
	}

	if accessKey != "" && secretKey != "" {
		cfgOpts = append(cfgOpts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	// PLATFORMCRAFT_HTTP_DEBUG=1 включает журналирование каждого HTTP-запроса и
	// ответа AWS SDK (метод, URL, все заголовки, включая x-amz-request-id) в
	// stderr плагина. Terraform пишет stderr плагина в свой лог, поэтому вместе с
	// TF_LOG=DEBUG и TF_LOG_PATH=... это даёт сырые заголовки ответов для
	// диагностики и обращений в поддержку.
	// Секретный ключ в лог не попадает: в заголовке Authorization только Access Key
	// ID и подпись. Тела запросов/ответов не пишутся.
	if os.Getenv("PLATFORMCRAFT_HTTP_DEBUG") != "" {
		cfgOpts = append(cfgOpts,
			config.WithClientLogMode(aws.LogRequest|aws.LogResponse|aws.LogRetries),
			config.WithLogger(logging.NewStandardLogger(os.Stderr)),
		)
	}

	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	}), nil
}

func (p *platformcraftProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBucketResource,
		NewBucketVersioningResource,
		NewBucketAclResource,
		NewBucketPolicyResource,
		NewBucketCorsResource,
		NewBucketObjectLockConfigurationResource,
		NewObjectResource,
		NewObjectCopyResource,
		NewObjectRetentionResource,
		NewObjectLegalHoldResource,
		// Multipart не выносится в отдельный ресурс: это деталь реализации
		// PutObject (manager.Uploader сам решает, когда делить на части).
		// DeleteObjects (batch) тоже не оборачивается: Terraform и так удаляет
		// N объектов параллельно.
	}
}

func (p *platformcraftProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewBucketsDataSource,
		NewBucketDataSource,
		NewBucketVersioningDataSource,
		NewBucketAclDataSource,
		NewBucketPolicyDataSource,
		NewBucketCorsDataSource,
		NewBucketObjectLockConfigurationDataSource,
		NewBucketObjectsDataSource,
		NewBucketObjectsV1DataSource,
		NewObjectDataSource,
		NewObjectAclDataSource,
		NewObjectVersionsDataSource,
	}
}

func (p *platformcraftProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	// Presigned URL — одноразовые секреты с TTL, поэтому это ephemeral resources
	// (Terraform >= 1.10): они не сохраняются ни в plan, ни в state.
	return []func() ephemeral.EphemeralResource{
		NewObjectPresignedGetUrlEphemeralResource,
		NewObjectPresignedPutUrlEphemeralResource,
	}
}

func stringOrEnv(v types.String, envVar, def string) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	if fromEnv := os.Getenv(envVar); fromEnv != "" {
		return fromEnv
	}
	return def
}

// isNotFound определяет, что ошибка API означает «бакета/объекта не существует»,
// чтобы Read() убрал ресурс из state (resp.State.RemoveResource) вместо ошибки.
// Коды «не найдено» для отдельных подресурсов (NoSuchBucketPolicy,
// NoSuchCORSConfiguration и т.д.) проверяются в соответствующих ресурсах.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket", "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}
