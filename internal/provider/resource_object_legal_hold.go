package provider

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// platformcraft_object_legal_hold — legal hold объекта. В отличие от retention (есть срок и режим GOVERNANCE/
// COMPLIANCE), legal hold — простой рубильник ON/OFF без даты, снимаемый в любой момент
// тем, у кого есть права s3:PutObjectLegalHold — никакой семантики "нельзя досрочно
// снять" здесь нет, поэтому Delete у этого ресурса всегда реально снимает hold.

var (
	_ resource.Resource                = &objectLegalHoldResource{}
	_ resource.ResourceWithConfigure   = &objectLegalHoldResource{}
	_ resource.ResourceWithImportState = &objectLegalHoldResource{}
)

func NewObjectLegalHoldResource() resource.Resource {
	return &objectLegalHoldResource{}
}

type objectLegalHoldResource struct {
	client *s3.Client
}

type objectLegalHoldResourceModel struct {
	Bucket  types.String `tfsdk:"bucket"`
	Key     types.String `tfsdk:"key"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Id      types.String `tfsdk:"id"`
}

func (r *objectLegalHoldResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_legal_hold"
}

func (r *objectLegalHoldResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Управляет legal hold объекта (put/get-object-legal-hold). В отличие от retention, у legal hold нет срока: он включается и снимается в любой момент, в том числе при удалении ресурса. Требует включённого Object Lock на бакете.",
		Attributes: map[string]schema.Attribute{
			"bucket": schema.StringAttribute{
				Required:    true,
				Description: "Имя бакета.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required:    true,
				Description: "Ключ объекта.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"enabled": schema.BoolAttribute{
				Required:    true,
				Description: "true — hold установлен (ON), объект нельзя удалить/перезаписать; false — снят (OFF).",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Идентификатор ресурса в Terraform state (bucket/key).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *objectLegalHoldResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*s3.Client)
	if !ok {
		resp.Diagnostics.AddError("Неожиданный тип ProviderData", "Ожидался *s3.Client, это внутренняя ошибка провайдера.")
		return
	}
	r.client = client
}

func (r *objectLegalHoldResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectLegalHoldResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.PutObjectLegalHold(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), plan.Enabled.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Ошибка установки legal hold объекта", err.Error())
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectLegalHoldResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectLegalHoldResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := pcs3.GetObjectLegalHold(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString())
	if err != nil {
		if isNotFound(err) || isNoSuchObjectLockConfiguration(err) {
			// Тот же класс ошибки, что и у GetObjectRetention (см. комментарий
			// к isNoSuchObjectLockConfiguration в resource_object_retention.go) —
			// 400 NoSuchObjectLockConfiguration, не 404, поэтому isNotFound() сам
			// по себе его не ловит.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Ошибка получения статуса legal hold объекта", err.Error())
		return
	}

	state.Enabled = types.BoolValue(status == s3types.ObjectLockLegalHoldStatusOn)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *objectLegalHoldResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan objectLegalHoldResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := pcs3.PutObjectLegalHold(ctx, r.client, plan.Bucket.ValueString(), plan.Key.ValueString(), plan.Enabled.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Ошибка изменения legal hold объекта", err.Error())
		return
	}

	plan.Id = types.StringValue(plan.Bucket.ValueString() + "/" + plan.Key.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *objectLegalHoldResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectLegalHoldResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// В отличие от retention/Object Lock, снять legal hold можно всегда — это и
	// есть штатное поведение Delete здесь, не заглушка с предупреждением.
	if err := pcs3.PutObjectLegalHold(ctx, r.client, state.Bucket.ValueString(), state.Key.ValueString(), false); err != nil {
		resp.Diagnostics.AddError("Ошибка снятия legal hold при удалении ресурса", err.Error())
		return
	}
}

func (r *objectLegalHoldResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// terraform import platformcraft_object_legal_hold.example <bucket>,<key>
	parts := splitTwo(req.ID)
	if parts == nil {
		resp.Diagnostics.AddError(
			"Некорректный формат ID для импорта",
			`Ожидается "<bucket>,<key>", например: terraform import platformcraft_object_legal_hold.example my-bucket,path/to/file.txt`,
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bucket"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[0]+"/"+parts[1])...)
}
