package provider

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_object — метаданные (а по запросу — и содержимое) уже
// существующего объекта (HeadObject/GetObject), без обязательной привязки к
// ресурсу platformcraft_object этого же state.
//
// Размер содержимого в content ограничен maxInlineContentBytes: сообщение
// между Terraform core и провайдером не может превышать ~256 МиБ, и большой
// объект уронил бы плагин с gRPC ResourceExhausted. Для больших объектов —
// download_path: потоковое скачивание на диск, в state содержимое не попадает.

var (
	_ datasource.DataSource              = &objectDataSource{}
	_ datasource.DataSourceWithConfigure = &objectDataSource{}
)

func NewObjectDataSource() datasource.DataSource {
	return &objectDataSource{}
}

type objectDataSource struct {
	client *s3.Client
}

type objectDataSourceModel struct {
	Bucket        types.String `tfsdk:"bucket"`
	Key           types.String `tfsdk:"key"`
	Id            types.String `tfsdk:"id"`
	Etag          types.String `tfsdk:"etag"`
	ContentType   types.String `tfsdk:"content_type"`
	ContentLength types.Int64  `tfsdk:"content_length"`
	LastModified  types.String `tfsdk:"last_modified"`
	ReadContent   types.Bool   `tfsdk:"read_content"`
	DownloadPath  types.String `tfsdk:"download_path"`
	Content       types.String `tfsdk:"content"`
}

func (d *objectDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object"
}

func (d *objectDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Читает метаданные (а по запросу — и содержимое) уже существующего объекта в бакете PlatformCraft.",
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
			"etag": schema.StringAttribute{
				Computed:    true,
				Description: "ETag объекта.",
			},
			"content_type": schema.StringAttribute{
				Computed:    true,
				Description: "Content-Type объекта.",
			},
			"content_length": schema.Int64Attribute{
				Computed:    true,
				Description: "Размер объекта в байтах.",
			},
			"last_modified": schema.StringAttribute{
				Computed:    true,
				Description: "Дата последнего изменения объекта (RFC3339).",
			},
			"read_content": schema.BoolAttribute{
				Optional: true,
				Description: fmt.Sprintf(
					"Если true, содержимое объекта записывается в content. По умолчанию false. "+
						"Только для объектов до %d МиБ: значение целиком передаётся между Terraform и "+
						"провайдером, а протокол плагинов ограничивает размер сообщения. "+
						"Для больших объектов используйте download_path.",
					maxInlineContentBytes/1024/1024,
				),
			},
			"download_path": schema.StringAttribute{
				Optional:    true,
				Description: "Путь к локальному файлу, в который объект скачивается потоково, минуя state, — без ограничения на размер. Если задан, read_content не действует и content остаётся пустым. Файл перезаписывается при каждом чтении data source (plan и apply).",
			},
			"content": schema.StringAttribute{
				Computed:    true,
				Description: "Содержимое объекта как строка. Заполняется только если read_content=true и download_path не задан.",
			},
		},
	}
}

func (d *objectDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *objectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucket := data.Bucket.ValueString()
	key := data.Key.ValueString()

	head, err := pcs3.HeadObject(ctx, d.client, bucket, key)
	if err != nil {
		resp.Diagnostics.AddError("Объект не найден или недоступен", err.Error())
		return
	}

	data.Id = types.StringValue(bucket + "/" + key)
	data.Etag = types.StringValue(aws.ToString(head.ETag))
	data.ContentType = types.StringValue(aws.ToString(head.ContentType))
	var size int64
	if head.ContentLength != nil {
		size = *head.ContentLength
		data.ContentLength = types.Int64Value(size)
	}
	if head.LastModified != nil {
		data.LastModified = types.StringValue(head.LastModified.Format("2006-01-02T15:04:05Z07:00"))
	}

	data.Content = types.StringValue("")

	downloadPath := data.DownloadPath.ValueString()
	wantContent := !data.ReadContent.IsNull() && data.ReadContent.ValueBool()

	switch {
	case downloadPath != "":
		// ВАЖНО: этот data source читается на каждый terraform plan/refresh, если его
		// входы уже известны — не только на apply. С download_path это значит, что файл
		// реально перекачивается на диск при каждом plan, даже если вы просто смотрите,
		// что изменится, без намерения запускать apply. Предупреждаем явно в выводе,
		// а не только в описании схемы — молчаливый побочный эффект на plan легко упустить.
		resp.Diagnostics.AddWarning(
			"download_path скачивает объект при каждом plan/refresh, не только при apply",
			"Data source читается на каждый terraform plan, если его входы уже известны. Объект "+bucket+"/"+key+
				" был реально перекачан на диск в "+downloadPath+" сейчас — если это нежелательно для рутинных "+
				"plan-без-apply, оберните data source в условие (count/for_each по переменной) вместо безусловного использования.",
		)

		// Потоковое скачивание на диск: тело ответа никогда целиком не лежит в
		// памяти процесса-провайдера и никогда не попадает в state/gRPC — размер
		// объекта здесь не ограничен лимитом сообщений Terraform core<->plugin.
		body, err := pcs3.GetObject(ctx, d.client, bucket, key)
		if err != nil {
			resp.Diagnostics.AddError("Ошибка скачивания объекта в download_path", err.Error())
			return
		}
		defer body.Close()

		f, err := os.Create(downloadPath)
		if err != nil {
			resp.Diagnostics.AddError("Не удалось создать файл по пути download_path", err.Error())
			return
		}
		_, copyErr := io.Copy(f, body)
		closeErr := f.Close()
		if copyErr != nil {
			resp.Diagnostics.AddError("Ошибка записи объекта в download_path", copyErr.Error())
			return
		}
		if closeErr != nil {
			resp.Diagnostics.AddError("Ошибка закрытия файла download_path", closeErr.Error())
			return
		}

	case wantContent:
		if size > maxInlineContentBytes {
			resp.Diagnostics.AddError(
				"Объект слишком большой для content",
				fmt.Sprintf(
					"Размер объекта %s/%s — %d байт, это больше безопасного порога %d байт для строкового "+
						"атрибута state. content целиком проходит через gRPC-канал между Terraform core и "+
						"провайдером, а тот ограничен ~256 МиБ на сообщение (ограничение terraform-plugin-go, "+
						"не PlatformCraft) — попытка прочитать такой объект в content уронит провайдер с "+
						"ошибкой ResourceExhausted вместо этого диагностического сообщения. "+
						"Задайте download_path вместо read_content, чтобы скачать объект на диск.",
					bucket, key, size, maxInlineContentBytes,
				),
			)
			return
		}

		body, err := pcs3.GetObject(ctx, d.client, bucket, key)
		if err != nil {
			resp.Diagnostics.AddError("Ошибка скачивания содержимого объекта", err.Error())
			return
		}
		defer body.Close()

		buf, err := io.ReadAll(io.LimitReader(body, maxInlineContentBytes+1))
		if err != nil {
			resp.Diagnostics.AddError("Ошибка чтения содержимого объекта", err.Error())
			return
		}
		if int64(len(buf)) > maxInlineContentBytes {
			// HeadObject соврал про размер (или объект дозаписали между HeadObject
			// и GetObject) — подстраховка от того же ResourceExhausted постфактум.
			resp.Diagnostics.AddError(
				"Объект оказался больше заявленного в HeadObject размера",
				"Фактическое содержимое превысило безопасный порог для content уже в процессе скачивания. "+
					"Используйте download_path вместо read_content.",
			)
			return
		}
		data.Content = types.StringValue(string(buf))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
