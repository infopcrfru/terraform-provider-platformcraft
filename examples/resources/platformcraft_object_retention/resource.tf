# Retention доступен для объектов в бакете с включённым Object Lock.
resource "platformcraft_object_retention" "example" {
  bucket       = "my-company-archive"
  key          = "contracts/2026/agreement.pdf"
  mode         = "GOVERNANCE"
  retain_until = "2030-01-01T00:00:00Z"

  # Разрешить сокращение срока GOVERNANCE-retention при изменении ресурса.
  bypass_governance_retention = true
}
