# Resources of the "acme" provider, known only to testdata/catalogs/plus-one (US8, SC-007).
# acme_widget is a verified catalog service; acme_gadget is a draft (unverified) one.

resource "acme_widget" "orders" {
  name = "orders"
  size = "large"
}

resource "acme_gadget" "notifier" {
  name   = "notifier"
  widget = acme_widget.orders.id
}
