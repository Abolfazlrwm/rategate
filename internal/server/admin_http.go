// Package server
//
// admin_http.go — REST admin API for runtime policy management.
//
// Routes (all under /v1/admin, subject to the same AuthMiddleware as /v1/check
// when server.api_key is set):
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
// given engine. It is called from HTTPServer.Engine().
func (h *HTTPServer) registerAdminRoutes(r *gin.Engine) {
	g := r.Group("/v1/admin/policies")
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
	c.JSON(http.StatusOK, gin.H{"policies": out})
}

func (h *HTTPServer) handleCreatePolicy(c *gin.Context) {
	var body policyJSON
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.core.Policies().Create(body.toConfig()); err != nil {
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, policyToJSON(body.toConfig()))
}

func (h *HTTPServer) handleGetPolicy(c *gin.Context) {
	name := c.Param("name")
	p, ok := h.core.Policies().Get(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": admin.ErrPolicyNotFound{Name: name}.Error()})
		return
	}
	c.JSON(http.StatusOK, policyToJSON(p))
}

func (h *HTTPServer) handleUpdatePolicy(c *gin.Context) {
	name := c.Param("name")
	var body policyJSON
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The name in the body must match the resource path; this prevents an
	// accidental rename via PUT (which would orphan the old name).
	if body.Name == "" {
		body.Name = name
	}
	if body.Name != name {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name in body must match name in path"})
		return
	}
	if err := h.core.Policies().Update(body.toConfig()); err != nil {
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, policyToJSON(body.toConfig()))
}

func (h *HTTPServer) handleDeletePolicy(c *gin.Context) {
	name := c.Param("name")
	if err := h.core.Policies().Delete(name); err != nil {
		c.JSON(adminStatusFor(err), gin.H{"error": err.Error()})
		return
	}
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
