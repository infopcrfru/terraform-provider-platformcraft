package provider

import (
	"errors"

	"github.com/aws/smithy-go"
)

// isUnsupportedAclConfiguration — API отклонил ACL для бакета или объекта.
// Используется везде, где провайдер работает с ACL; текст подсказки —
// unsupportedAclHint.
func isUnsupportedAclConfiguration(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "UnsupportedAclConfigurationException" || apiErr.ErrorCode() == "AccessControlListNotSupported"
	}
	return false
}

const unsupportedAclHint = "API отклонил ACL для этого бакета или объекта. По документации PlatformCraft put-bucket-acl и " +
	"put-object-acl работают без предварительной настройки, и на новых бакетах ACL работает. Проверьте ACL на новом " +
	"бакете; если там он работает, обратитесь в поддержку PlatformCraft с именем этого бакета."
