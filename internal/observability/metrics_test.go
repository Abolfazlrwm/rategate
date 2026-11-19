package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestMetrics_AdminOpsAndPolicyCount verifies the admin operation counter and
// the policy-count gauge record values correctly.
func TestMetrics_AdminOpsAndPolicyCount(t *testing.T) {
	m, _ := NewMetrics()

	m.RecordAdminOp("create", "success")
	m.RecordAdminOp("create", "success")
	m.RecordAdminOp("delete", "error")
	m.SetPolicyCount(5)

	if got := testutil.ToFloat64(m.AdminOpsTotal.WithLabelValues("create", "success")); got != 2 {
		t.Fatalf("admin_operations_total{create,success}=%v want 2", got)
	}
	if got := testutil.ToFloat64(m.AdminOpsTotal.WithLabelValues("delete", "error")); got != 1 {
		t.Fatalf("admin_operations_total{delete,error}=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Policies); got != 5 {
		t.Fatalf("rategate_policies=%v want 5", got)
	}
}
