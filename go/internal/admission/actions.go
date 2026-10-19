// Package admission holds the router's shared action/resource labels.
package admission

const (
	Namespace         = "abstraction.router"
	ActionInventory   = "abstraction.router/inventory.read"
	ResourceInventory = "abstraction.router/inventory"
	ActionRoute       = "abstraction.router/route"
	ResourceRoutes    = "abstraction.router/routes"
	CodeForbidden     = "forbidden"
)
