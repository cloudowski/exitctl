# Types from providers that the catalog does not know.

resource "acme_widget" "core" {
  name = "core"
  size = "large"
}

resource "acme_widget" "edge" {
  name      = "edge"
  parent_id = acme_widget.core.id
}

resource "exoticcloud_bucket" "assets" {
  name      = "assets"
  widget_id = acme_widget.core.id
}

resource "exoticcloud_bucket" "logs" {
  name      = "logs"
  widget_id = acme_widget.core.id
}
