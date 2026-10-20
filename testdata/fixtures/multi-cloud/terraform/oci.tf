variable "compartment_id" {
  type = string
}

resource "oci_database_autonomous_database" "adb" {
  compartment_id           = var.compartment_id
  db_name                  = "shopadb"
  display_name             = "shop-adb"
  db_workload              = "OLTP"
  compute_model            = "ECPU"
  compute_count            = 2
  data_storage_size_in_tbs = 1
}
