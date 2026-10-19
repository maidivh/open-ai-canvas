package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerSharedWalletRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/wallet-settings", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.AdminWalletSettings(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PUT("/admin/wallet-settings", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.WalletSettingsRequest
		if err = c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		view, err := svc.UpdateWalletSettings(c.Request.Context(), actor, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.POST("/admin/wallet-reconcile", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		count, err := svc.ReconcileSharedWallet(c.Request.Context(), actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"reconciledCount": count})
	})
}
