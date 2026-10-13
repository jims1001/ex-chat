package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *AuthEnterpriseHandler) SetBillingPortalService(service *billingportal.Service) {
	h.billingPortalService = service
}

// Strict local parsing rejects ambiguous JSON without changing Gin globally.
// Neither URL queries nor client-supplied provider/customer/configuration/flow
// fields can cross this boundary. No request value is included in an error.
func readBillingObject(c *gin.Context) (map[string]json.RawMessage, error) {
	if c.Request.URL.RawQuery != "" {
		return nil, errors.New("query not accepted")
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	if err != nil || len(body) > 4096 {
		return nil, errors.New("invalid body")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("object required")
	}
	result := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid key")
		}
		if _, ok = result[key]; ok {
			return nil, errors.New("duplicate key")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		result[key] = value
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return result, nil
}
func (h *AuthEnterpriseHandler) billingPortalError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "Billing provider unavailable"
	switch {
	case errors.Is(err, billingportal.ErrCurrencyRequired):
		status, message = http.StatusUnprocessableEntity, "Explicit billing currency selection required"
	case errors.Is(err, billingportal.ErrInvalidRequest):
		status, message = http.StatusUnprocessableEntity, "Invalid billing request or unsupported currency"
	case errors.Is(err, billingportal.ErrConflict):
		status, message = http.StatusConflict, "Billing customer requires reconciliation"
	case errors.Is(err, billingportal.ErrCustomerRequired):
		status, message = http.StatusConflict, "Billing customer initialization required"
	}
	data := gin.H{"success": false, "error": message}
	if errors.Is(err, billingportal.ErrCurrencyRequired) {
		data["currency_selection_required"] = true
		data["currency_options"] = h.billingPortalService.CurrencyOptions()
		data["data"] = gin.H{"billing_customer_status": "currency_required", "currency_selection_required": true, "currency_options": h.billingPortalService.CurrencyOptions()}
	}
	c.JSON(status, data)
}
func (h *AuthEnterpriseHandler) initializeBillingCustomer(c *gin.Context, account uint) {
	record, err := h.billingPortalService.Initialize(c.Request.Context(), account)
	if err != nil {
		h.billingPortalError(c, err)
		return
	}
	response.Success(c, gin.H{"billing_customer_status": "ready", "selected_billing_currency": record.Currency, "billing_currency": record.Currency, "currency": record.Currency, "currency_selection_required": false, "currency_options": h.billingPortalService.CurrencyOptions()})
}
func (h *AuthEnterpriseHandler) Checkout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	account, err := h.getAccountIDParam(c)
	if err != nil || account == 0 {
		response.BadRequest(c, "Account ID must be positive and numeric")
		return
	}
	request, err := readBillingObject(c)
	if err != nil || len(request) != 0 {
		response.BadRequest(c, "Checkout accepts no client parameters")
		return
	}
	portal, currency, err := h.billingPortalService.Checkout(c.Request.Context(), account)
	if err != nil {
		h.billingPortalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "redirect_url": portal.URL, "currency": currency, "selected_billing_currency": currency, "data": gin.H{"redirect_url": portal.URL, "currency": currency, "billing_currency": currency, "selected_billing_currency": currency}})
}
func (h *AuthEnterpriseHandler) SelectBillingCurrency(c *gin.Context) {
	account, err := h.getAccountIDParam(c)
	if err != nil || account == 0 {
		response.BadRequest(c, "Account ID must be positive and numeric")
		return
	}
	request, err := readBillingObject(c)
	if err != nil || len(request) != 1 {
		response.BadRequest(c, "Specify one billing currency")
		return
	}
	var currency string
	if json.Unmarshal(request["currency"], &currency) != nil || currency == "" {
		response.BadRequest(c, "Invalid billing currency")
		return
	}
	// Preserve the existing explicit ASCII case/space input compatibility, but
	// never turn blank/invalid input into a default or infer a locale mapping.
	currency = strings.TrimSpace(currency)
	for _, letter := range currency {
		if (letter < 'a' || letter > 'z') && (letter < 'A' || letter > 'Z') {
			response.BadRequest(c, "Invalid billing currency")
			return
		}
	}
	currency = strings.ToLower(currency)
	if err = h.billingPortalService.SelectCurrency(c.Request.Context(), account, currency); err != nil {
		h.billingPortalError(c, err)
		return
	}
	data := gin.H{"selected_billing_currency": currency, "billing_currency": currency, "currency": currency, "currency_selection_required": false, "currency_options": h.billingPortalService.CurrencyOptions()}
	c.JSON(http.StatusOK, gin.H{"success": true, "selected_billing_currency": currency, "billing_currency": currency, "message": "Billing currency updated successfully", "data": data})
}
