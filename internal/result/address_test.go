package result

import "testing"

func TestDisplayAddress(t *testing.T) {
	cases := []struct {
		n    Node
		want string
	}{
		{Node{ID: "orders-app//#component", Kind: "component", NativeAddress: "."}, "component orders-app"},
		{Node{ID: "shop//services/orders#component", Kind: "component", NativeAddress: "services/orders"}, "component services/orders"},
		{Node{ID: "shop//k8s#/Namespace/_/shop", Kind: "resource", NativeAddress: "/Namespace/_/shop"}, "Namespace/_/shop"},
		{Node{ID: "shop//k8s#apps/Deployment/shop/web", Kind: "resource", NativeAddress: "apps/Deployment/shop/web"}, "apps/Deployment/shop/web"},
		{Node{ID: "implied//aws.sqs", Kind: "implied_service", Service: "aws.sqs"}, "aws.sqs (used by code, not declared)"},
		{Node{ID: "x//#y", Kind: "resource"}, "x//#y"},
	}
	for _, c := range cases {
		if got := DisplayAddress(&c.n); got != c.want {
			t.Errorf("DisplayAddress(%s) = %q, want %q", c.n.ID, got, c.want)
		}
	}
}
