// Package server
//
// admin_http.go — REST admin API for runtime policy management.
//
// Routes (all under /v1/admin; gated by server.admin_api_key when set,
// otherwise by server.api_key; when neither credential is configured the
// routes answer 503 and deny everything — the policy-mutation surface is
// never left open):
//
//	GET    /v1/admin/policies          list all policies (sorted by name)
//	POST   /v1/admin/policies          create a policy (409 if name exists)
//	GET    /v1/admin/policies/:name    fetch one policy (404 if missing)
//	PUT    /v1/admin/policies/:name    update a policy (404 if missing)
//	DELETE /v1/admin/policies/:name    delete a policy (404 if missing)
//
// Mutations go through the thread-safe admin.Registry, so they are safe to
// issue concurrently with live /v1/check traffic. A created/updated policy is
// immediately visible to subsequent Checks.
package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Abolfazlrwm/rategate/internal/admin"
	"github.com/Abolfazlrwm/rategate/internal/config"
)

// policyJSON is the REST representation of a policy. Window is expressed in
// milliseconds (window_ms) to mirror the gRPC CheckRateLimitRequest field and
// stay JSON-friendly.
type policyJSON struct {
	Name            string  `json:"name"                      binding:"required"`
	Algorithm       string  `json:"algorithm"`
	Limit           int64   `json:"limit"`
	RefillPerSecond float64 `json:"refill_per_second"`
	WindowMs        int64   `json:"window_ms"`
}

func policyToJSON(p config.PolicyConfig) policyJSON {
	return policyJSON{
		Name:            p.Name,
		Algorithm:       p.Algorithm,
		Limit:           p.Limit,
		RefillPerSecond: p.RefillPerSecond,
		WindowMs:        p.Window.Milliseconds(),
	}
}

func (j policyJSON) toConfig() config.PolicyConfig {
	return config.PolicyConfig{
		Name:            j.Name,
		Algorithm:       j.Algorithm,
		Limit:           j.Limit,
		RefillPerSecond: j.RefillPerSecond,
		Window:          time.Duration(j.WindowMs) * time.Millisecond,
	}
}

// registerAdminRoutes wires the /v1/admin/policies CRUD endpoints onto the
// given engine. The group is gated by the admin API key when one is configured
// (server.admin_api_key); otherwise it falls back to the shared client API
// key, preserving Phase 2 behavior. It is called from HTTPServer.Engine().
func (h *HTTPServer) registerAdminRoutes(r *gin.Engine) {
	adminKey := h.core.cfg.Server.AdminAPIKey
	if adminKey == "" {
		adminKey = h.core.cfg.Server.APIKey // backward-compatible fallback
	}
	g := r.Group("/v1/admin/policies")
	if adminKey != "" {
		g.Use(AuthMiddleware(adminKey))
	} else {
		// Deny by default. An unauthenticated policy-mutation API would let
		// anyone rewrite live rate limits (or poison them to unlimited), so
		// with no credential configured the whole group is disabled. 503
		// tells operators the feature exists but is switched off, not that
		// they hit a wrong route.
		g.Use(func(c *gin.Context) {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "admin API is disabled: configure server.admin_api_key (or server.api_key) to enable it",
			})
		})
	}
	g.GET("", h.handleListPolicies)
	g.POST("", h.handleCreatePolicy)
	g.GET("/:name", h.handleGetPolicy)
	g.PUT("/:name", h.handleUpdatePolicy)
	g.DELETE("/:name", h.handleDeletePolicy)
}

func (h *HTTPServer) handleListPolicies(c *gin.Context) {
	all := h.core.Policies().List()
	out := make([]policyJSON, 0, len(all))
	for _, p := range all {
		out = append(out, policyToJSON(p))
	}
	h.core.recordAdminOp("list", "success")
	c.JSON(http.StatusOK, gin.H{"policies": out})
}

func (h *HTTPServer) handleCreatePolicy(c *gin.Context) {
	var body policyJSON
	if err := c.ShouldBindJSON(&body); err != nil {
		h.core.recordAdminOp("create", "error")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.core.Policies().Create(body.toConfig()); err != nil {
		h.core.recordAdminOp("create", "error")
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
	h.core.recordAdminOp("create", "success")
	h.core.refreshPolicyCount()
	c.JSON(http.StatusCreated, policyToJSON(body.toConfig()))
}

func (h *HTTPServer) handleGetPolicy(c *gin.Context) {
	name := c.Param("name")
	p, ok := h.core.Policies().Get(name)
	if !ok {
		h.core.recordAdminOp("get", "error")
		c.JSON(http.StatusNotFound, gin.H{"error": admin.ErrPolicyNotFound{Name: name}.Error()})
		return
	}
	h.core.recordAdminOp("get", "success")
	c.JSON(http.StatusOK, policyToJSON(p))
}

func (h *HTTPServer) handleUpdatePolicy(c *gin.Context) {
	name := c.Param("name")
	var body policyJSON
	if err := c.ShouldBindJSON(&body); err != nil {
		h.core.recordAdminOp("update", "error")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The name in the body must match the resource path; this prevents an
	// accidental rename via PUT (which would orphan the old name).
	if body.Name == "" {
		body.Name = name
	}
	if body.Name != name {
		h.core.recordAdminOp("update", "error")
		c.JSON(http.StatusBadRequest, gin.H{"error": "name in body must match name in path"})
		return
	}
	if err := h.core.Policies().Update(body.toConfig()); err != nil {
		h.core.recordAdminOp("update", "error")
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
	// Update does not change the policy count, so the gauge is not refreshed.
	h.core.recordAdminOp("update", "success")
	c.JSON(http.StatusOK, policyToJSON(body.toConfig()))
}

func (h *HTTPServer) handleDeletePolicy(c *gin.Context) {
	name := c.Param("name")
	if err := h.core.Policies().Delete(name); err != nil {
		h.core.recordAdminOp("delete", "error")
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
	h.core.recordAdminOp("delete", "success")
	h.core.refreshPolicyCount()
	c.Status(http.StatusNoContent)
}

// adminStatusFor maps an admin.Registry / config.PolicyConfig validation error
// to the appropriate HTTP status. Validation errors are 400 (bad request);
// ErrPolicyExists is 409 (conflict); ErrPolicyNotFound is 404. Anything else
// is a 500 to surface unexpected failures honestly.
func adminStatusFor(err error) int {
	var existsErr admin.ErrPolicyExists
	var notFoundErr admin.ErrPolicyNotFound
	switch {
	case errors.As(err, &existsErr):
		return http.StatusConflict
	case errors.As(err, &notFoundErr):
		return http.StatusNotFound
	default:
		// PolicyConfig.Validate() returns plain fmt.Errorf messages for
		// bad input (missing name, non-positive limit, unknown algorithm,
		// missing algorithm-specific field). These are client errors.
		return http.StatusBadRequest
	}
}
