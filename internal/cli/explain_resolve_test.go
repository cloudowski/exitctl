package cli

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
)

func TestResolveTargetByLocation(t *testing.T) {
	ids := []string{
		"repo//manifests/modules/security/irsa/.workshop/terraform#aws_kms_key.cmk_dynamodb",
		"repo//manifests/modules/security/eks-pod-identity/.workshop/terraform#aws_kms_key.cmk_dynamodb",
	}
	doc := &result.Document{}
	for _, id := range ids {
		doc.Graph.Nodes = append(doc.Graph.Nodes, result.Node{ID: id, NativeAddress: "aws_kms_key.cmk_dynamodb"})
	}
	// The bare address is ambiguous.
	if _, _, err := resolveTarget(doc, "aws_kms_key.cmk_dynamodb"); err == nil {
		t.Fatal("ambiguous address resolved")
	}
	for arg, want := range map[string]string{
		"security/irsa: aws_kms_key.cmk_dynamodb":   ids[0],
		"eks-pod-identity:aws_kms_key.cmk_dynamodb": ids[1],
	} {
		n, _, err := resolveTarget(doc, arg)
		if err != nil || n.ID != want {
			t.Errorf("%q: got %v, %v; want %s", arg, n, err, want)
		}
	}
	// Locations match whole path segments only.
	if _, _, err := resolveTarget(doc, "rsa: aws_kms_key.cmk_dynamodb"); err == nil {
		t.Fatal("partial segment matched")
	}
}
