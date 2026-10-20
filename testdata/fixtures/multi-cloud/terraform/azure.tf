resource "azurerm_resource_group" "rg" {
  name     = "shop"
  location = "westeurope"
}

resource "azurerm_cosmosdb_account" "acc" {
  name                = "shop-cosmos"
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  offer_type          = "Standard"
  kind                = "GlobalDocumentDB"

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = azurerm_resource_group.rg.location
    failover_priority = 0
  }
}

resource "azurerm_servicebus_namespace" "bus" {
  name                = "shop-bus"
  location            = azurerm_resource_group.rg.location
  resource_group_name = azurerm_resource_group.rg.name
  sku                 = "Standard"
}

resource "azurerm_servicebus_queue" "q" {
  name         = "orders"
  namespace_id = azurerm_servicebus_namespace.bus.id
}
