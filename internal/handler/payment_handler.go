package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"github.com/gin-gonic/gin"
)

func (h *AuthEnterpriseHandler) SetPaymentService(s *payment.Service) { h.paymentService = s }

func (h *AuthEnterpriseHandler) TopupCheckout(c *gin.Context) {
	accountID, err := h.getAccountIDParam(c)
	if err != nil || accountID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Account ID must be positive and numeric"})
		return
	}
	var req struct {
		Credits  int64  `json:"credits"`
		Currency string `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Invalid top-up request"})
		return
	}
	if _, ok := payment.TopupPrice(req.Credits); !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Select a supported credit package"})
		return
	}
	// Prices are explicitly USD-only. A supplied currency must agree rather than
	// merely relabeling a USD amount as another currency.
	if req.Currency == "" {
		req.Currency = "usd"
	}
	req.Currency = strings.ToLower(strings.TrimSpace(req.Currency))
	if req.Currency != "usd" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Only USD top-ups are supported"})
		return
	}
	if !h.paymentService.Ready() {
		paymentError(c, payment.ErrUnavailable)
		return
	}
	intent, err := h.paymentService.CheckoutTopup(c.Request.Context(), accountID, req.Credits, req.Currency, c.GetHeader("Idempotency-Key"))
	if err != nil {
		paymentError(c, err)
		return
	}
	data := gin.H{"id": accountID, "intent_id": intent.IntentID, "session_id": intent.SessionID, "credits": intent.CreditUnits, "amount_minor": intent.AmountMinor, "amount": float64(intent.AmountMinor) / 100, "currency": intent.Currency, "redirect_url": intent.CheckoutURL, "status": intent.Status, "payment_type": "one_time"}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": accountID, "credits": intent.CreditUnits, "amount_minor": intent.AmountMinor, "amount": float64(intent.AmountMinor) / 100, "currency": intent.Currency, "redirect_url": intent.CheckoutURL, "status": intent.Status, "data": data})
}

func (h *AuthEnterpriseHandler) HandleSubscriptionWebhook(c *gin.Context) {
	if !h.paymentService.Ready() {
		paymentError(c, payment.ErrUnavailable)
		return
	}
	// Verify the exact body before decoding. The endpoint historically named
	// subscription/webhook now accepts verified top-up Checkout events only.
	const maxBody = 256 << 10
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid webhook body"})
		return
	}
	if err = h.paymentService.HandleWebhook(c.Request.Context(), body, c.GetHeader("Stripe-Signature")); err != nil {
		paymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "received": true})
}
func paymentError(c *gin.Context, err error) {
	code := http.StatusServiceUnavailable
	message := "Payment processing is temporarily unavailable"
	switch {
	case errors.Is(err, payment.ErrInvalidRequest):
		code = http.StatusUnprocessableEntity
		message = "Invalid payment request; use a supported package, USD, and an Idempotency-Key of 8 to 128 characters"
	case errors.Is(err, payment.ErrInvalidWebhook):
		code = http.StatusBadRequest
		message = "Invalid webhook signature or payload"
	case errors.Is(err, payment.ErrMismatch):
		code = http.StatusBadRequest
		message = "Payment does not match a stored checkout"
	case errors.Is(err, payment.ErrConflict):
		code = http.StatusConflict
		message = "Payment idempotency conflict"
	}
	c.JSON(code, gin.H{"success": false, "error": message})
}
