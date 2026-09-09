package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestContactCRM_Enhancement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_contact_crm_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	// 1. Register test admin user
	signUpPayload := map[string]any{
		"account_name": "Acme Global CRM",
		"name":         "CRM Director",
		"email":        "crm.director@acme.com",
		"password":     "SecretPass123!",
	}
	body, _ := json.Marshal(signUpPayload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign_up expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token

	authHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}

	doReq := func(method, path string, payload any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
		var buf *bytes.Buffer
		if payload != nil {
			if s, ok := payload.(string); ok {
				buf = bytes.NewBufferString(s)
			} else {
				b, _ := json.Marshal(payload)
				buf = bytes.NewBuffer(b)
			}
		} else {
			buf = bytes.NewBuffer(nil)
		}

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, buf)
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
		for _, eh := range extraHeaders {
			for k, v := range eh {
				req.Header.Set(k, v)
			}
		}
		r.ServeHTTP(w, req)
		return w
	}

	getData := func(body []byte) any {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if d, ok := m["data"]; ok {
			return d
		}
		return m
	}

	getMap := func(body []byte) map[string]any {
		d := getData(body)
		if m, ok := d.(map[string]any); ok {
			return m
		}
		return nil
	}

	getSlice := func(body []byte) []any {
		d := getData(body)
		if s, ok := d.([]any); ok {
			return s
		}
		return nil
	}

	// ==========================================
	// 场景 1: 联系人标签 (Contact Labels)
	// ==========================================
	t.Run("Scenario1_ContactLabels_LifecycleAndFilter", func(t *testing.T) {
		// 1.1 创建联系人
		wCreate := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
			"name":         "Alice Morgan",
			"email":        "alice.m@example.com",
			"phone_number": "+15550101",
		})
		if wCreate.Code != http.StatusCreated {
			t.Fatalf("create contact expected 201, got %d: %s", wCreate.Code, wCreate.Body.String())
		}
		cRes := getMap(wCreate.Body.Bytes())
		contactID := uint(cRes["id"].(float64))

		// 1.2 为联系人设置标签 (VIP, Enterprise)
		wSetLabels := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/labels", contactID), map[string]any{
			"labels": []string{"VIP", "Enterprise"},
		})
		if wSetLabels.Code != http.StatusOK {
			t.Fatalf("set contact labels expected 200, got %d: %s", wSetLabels.Code, wSetLabels.Body.String())
		}
		labelsRes := getSlice(wSetLabels.Body.Bytes())
		if len(labelsRes) != 2 {
			t.Fatalf("expected 2 labels, got %d: %s", len(labelsRes), wSetLabels.Body.String())
		}

		// 1.3 获取联系人标签
		wGetLabels := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/labels", contactID), nil)
		if wGetLabels.Code != http.StatusOK {
			t.Fatalf("get contact labels expected 200, got %d: %s", wGetLabels.Code, wGetLabels.Body.String())
		}
		gotLabels := getSlice(wGetLabels.Body.Bytes())
		if len(gotLabels) != 2 {
			t.Fatalf("expected 2 labels returned, got %d", len(gotLabels))
		}
		var vipLabelID uint
		for _, it := range gotLabels {
			l := it.(map[string]any)
			if l["title"] == "VIP" {
				vipLabelID = uint(l["id"].(float64))
			}
		}
		if vipLabelID == 0 {
			t.Fatalf("VIP label ID not found")
		}

		// 1.4 按标签筛选联系人
		wFilter := doReq(http.MethodGet, "/api/v1/accounts/1/contacts?label=VIP", nil)
		if wFilter.Code != http.StatusOK {
			t.Fatalf("filter contacts by label expected 200, got %d: %s", wFilter.Code, wFilter.Body.String())
		}
		filterRes := getMap(wFilter.Body.Bytes())
		payloadList := filterRes["items"].([]any)
		if len(payloadList) != 1 {
			t.Fatalf("expected 1 contact with VIP label, got %d", len(payloadList))
		}

		// 1.5 解除单个标签 (VIP)
		wDetach := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/labels/%d", contactID, vipLabelID), nil)
		if wDetach.Code != http.StatusOK {
			t.Fatalf("detach contact label expected 200, got %d: %s", wDetach.Code, wDetach.Body.String())
		}

		// 验证仅剩 Enterprise
		wGetAfterDetach := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/labels", contactID), nil)
		labelsAfter := getSlice(wGetAfterDetach.Body.Bytes())
		if len(labelsAfter) != 1 || labelsAfter[0].(map[string]any)["title"] != "Enterprise" {
			t.Fatalf("expected only Enterprise label remaining, got %v", labelsAfter)
		}
	})

	// ==========================================
	// 场景 2: 联系人导出 (Contact Export)
	// ==========================================
	t.Run("Scenario2_ContactExport_CSV_And_JSON", func(t *testing.T) {
		// 2.1 创建具有中文名和标签的联系人
		wCreate := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
			"name":         "张三丰",
			"email":        "zhangsanfeng@wudang.com",
			"phone_number": "+8613800000000",
			"labels":       []string{"武当派", "VIP"},
		})
		if wCreate.Code != http.StatusCreated {
			t.Fatalf("create contact expected 201, got %d", wCreate.Code)
		}

		// 2.2 导出 CSV (验证 UTF-8 BOM 与表头)
		wExportCSV := doReq(http.MethodGet, "/api/v1/accounts/1/contacts/export?q=张三丰", nil)
		if wExportCSV.Code != http.StatusOK {
			t.Fatalf("export contacts CSV expected 200, got %d: %s", wExportCSV.Code, wExportCSV.Body.String())
		}
		csvBytes := wExportCSV.Body.Bytes()
		// 验证 UTF-8 BOM: \xEF\xBB\xBF
		if len(csvBytes) < 3 || csvBytes[0] != 0xEF || csvBytes[1] != 0xBB || csvBytes[2] != 0xBF {
			t.Fatalf("CSV does not begin with UTF-8 BOM")
		}
		csvText := string(csvBytes[3:])
		if !strings.Contains(csvText, "张三丰") || !strings.Contains(csvText, "zhangsanfeng@wudang.com") {
			t.Fatalf("CSV content missing exported contact details: %s", csvText)
		}
		if !strings.Contains(csvText, "武当派") {
			t.Fatalf("CSV content missing contact label: %s", csvText)
		}

		// 2.3 POST 方式自定义列导出并请求 JSON 格式
		wExportJSON := doReq(http.MethodPost, "/api/v1/accounts/1/contacts/export", map[string]any{
			"q":       "张三丰",
			"columns": []string{"name", "email", "labels"},
			"format":  "json",
		})
		if wExportJSON.Code != http.StatusOK {
			t.Fatalf("export contacts JSON expected 200, got %d: %s", wExportJSON.Code, wExportJSON.Body.String())
		}
		var exportJSONRes map[string]any
		_ = json.Unmarshal(wExportJSON.Body.Bytes(), &exportJSONRes)
		if exportJSONRes["total_count"].(float64) < 1 {
			t.Fatalf("expected total_count >= 1, got %v", exportJSONRes["total_count"])
		}
		rawCSV := exportJSONRes["csv_data"].(string)
		if !strings.Contains(rawCSV, "name,email,labels") {
			t.Fatalf("custom columns header missing: %s", rawCSV)
		}
	})

	// ==========================================
	// 场景 3: 联系人备注编辑/删除 (Notes Edit & Delete)
	// ==========================================
	t.Run("Scenario3_ContactNotes_CreateEditDelete", func(t *testing.T) {
		// 3.1 创建联系人
		wCreate := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
			"name":  "Bob Builder",
			"email": "bob@builder.com",
		})
		cRes := getMap(wCreate.Body.Bytes())
		contactID := uint(cRes["id"].(float64))

		// 3.2 创建备注
		wCreateNote := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes", contactID), map[string]any{
			"content": "客户正在进行私有化部署评估，意向强烈。",
		})
		if wCreateNote.Code != http.StatusCreated {
			t.Fatalf("create note expected 201, got %d: %s", wCreateNote.Code, wCreateNote.Body.String())
		}
		noteRes := getMap(wCreateNote.Body.Bytes())
		noteID := uint(noteRes["id"].(float64))
		if noteRes["user"] == nil {
			t.Fatalf("note response should preload user details")
		}

		// 3.3 查看备注列表
		wListNotes := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes", contactID), nil)
		if wListNotes.Code != http.StatusOK {
			t.Fatalf("list notes expected 200, got %d: %s", wListNotes.Code, wListNotes.Body.String())
		}
		notesList := getSlice(wListNotes.Body.Bytes())
		if len(notesList) != 1 {
			t.Fatalf("expected 1 note in list, got %d", len(notesList))
		}

		// 3.4 编辑备注 (PUT /contacts/:id/notes/:note_id)
		wUpdateNote := doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes/%d", contactID, noteID), map[string]any{
			"content": "已完成 POC 阶段，合同审批流程中。",
		})
		if wUpdateNote.Code != http.StatusOK {
			t.Fatalf("update note expected 200, got %d: %s", wUpdateNote.Code, wUpdateNote.Body.String())
		}
		updatedNote := getMap(wUpdateNote.Body.Bytes())
		if updatedNote["content"] != "已完成 POC 阶段，合同审批流程中。" {
			t.Fatalf("updated note content mismatch: %v", updatedNote["content"])
		}

		// 3.5 非法修改空内容校验
		wEmptyUpdate := doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes/%d", contactID, noteID), map[string]any{
			"content": "   ",
		})
		if wEmptyUpdate.Code != http.StatusBadRequest {
			t.Fatalf("empty note content expected 400, got %d", wEmptyUpdate.Code)
		}

		// 3.6 跨联系人或不存在的备注 404 拦截
		wFakeUpdate := doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes/99999", contactID), map[string]any{
			"content": "fake content",
		})
		if wFakeUpdate.Code != http.StatusNotFound {
			t.Fatalf("non-existent note update expected 404, got %d", wFakeUpdate.Code)
		}

		// 3.7 删除备注 (DELETE /contacts/:id/notes/:note_id)
		wDeleteNote := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes/%d", contactID, noteID), nil)
		if wDeleteNote.Code != http.StatusOK {
			t.Fatalf("delete note expected 200, got %d: %s", wDeleteNote.Code, wDeleteNote.Body.String())
		}

		// 再次列表验证已无备注
		wListAfterDelete := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes", contactID), nil)
		notesAfter := getSlice(wListAfterDelete.Body.Bytes())
		if len(notesAfter) != 0 {
			t.Fatalf("expected 0 notes after delete, got %d", len(notesAfter))
		}
	})

	// ==========================================
	// 场景 4: 公司与联系人关联管理 (Company & Contact Association)
	// ==========================================
	t.Run("Scenario4_Company_Contact_Association", func(t *testing.T) {
		// 4.1 创建公司
		wCreateComp := doReq(http.MethodPost, "/api/v1/accounts/1/companies", map[string]any{
			"name":        "Oracle BetX Group",
			"domain":      "oraclebetx.com",
			"industry":    "FinTech / Gaming",
			"description": "Next-gen sports entertainment platform",
		})
		if wCreateComp.Code != http.StatusCreated {
			t.Fatalf("create company expected 201, got %d: %s", wCreateComp.Code, wCreateComp.Body.String())
		}
		compRes := getMap(wCreateComp.Body.Bytes())
		companyID := uint(compRes["id"].(float64))

		// 4.2 创建联系人时直接绑定 company_id
		wCreateC1 := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
			"name":       "Charlie CTO",
			"email":      "charlie@oraclebetx.com",
			"company_id": companyID,
		})
		if wCreateC1.Code != http.StatusCreated {
			t.Fatalf("create contact with company_id expected 201, got %d: %s", wCreateC1.Code, wCreateC1.Body.String())
		}
		c1Res := getMap(wCreateC1.Body.Bytes())
		contact1ID := uint(c1Res["id"].(float64))
		if c1Res["company_id"] == nil || uint(c1Res["company_id"].(float64)) != companyID {
			t.Fatalf("contact company_id expected %d, got %v", companyID, c1Res["company_id"])
		}

		// 4.3 创建第二个联系人 (未关联公司)，随后通过公司关联接口批量绑定
		wCreateC2 := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
			"name":  "David COO",
			"email": "david@oraclebetx.com",
		})
		c2Res := getMap(wCreateC2.Body.Bytes())
		contact2ID := uint(c2Res["id"].(float64))

		// 4.4 调用公司关联接口批量绑定联系人
		wAssociate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/companies/%d/contacts", companyID), map[string]any{
			"contact_ids": []uint{contact2ID},
		})
		if wAssociate.Code != http.StatusOK {
			t.Fatalf("associate contacts with company expected 200, got %d: %s", wAssociate.Code, wAssociate.Body.String())
		}

		// 4.5 查询公司下的联系人列表
		wCompContacts := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/contacts", companyID), nil)
		if wCompContacts.Code != http.StatusOK {
			t.Fatalf("list company contacts expected 200, got %d: %s", wCompContacts.Code, wCompContacts.Body.String())
		}
		compContactsRes := getMap(wCompContacts.Body.Bytes())
		compContactsList := compContactsRes["items"].([]any)
		if len(compContactsList) != 2 {
			t.Fatalf("expected 2 contacts under company, got %d", len(compContactsList))
		}

		// 4.6 查询公司详情及列表包含 contacts_count 统计
		wGetComp := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d", companyID), nil)
		getCompRes := getMap(wGetComp.Body.Bytes())
		if getCompRes["contacts_count"] == nil || getCompRes["contacts_count"].(float64) != 2 {
			t.Fatalf("expected contacts_count == 2, got %v", getCompRes["contacts_count"])
		}

		// 4.7 解除单个联系人关联 (DELETE /companies/:id/contacts/:contact_id)
		wUnlink := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/companies/%d/contacts/%d", companyID, contact2ID), nil)
		if wUnlink.Code != http.StatusOK {
			t.Fatalf("unlink contact from company expected 200, got %d: %s", wUnlink.Code, wUnlink.Body.String())
		}

		// 验证 contact2 的 company_id 已置空
		wGetC2 := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d", contact2ID), nil)
		c2Fresh := getMap(wGetC2.Body.Bytes())
		if c2Fresh["company_id"] != nil {
			t.Fatalf("contact2 company_id should be nil after unlink, got %v", c2Fresh["company_id"])
		}

		// 4.8 删除公司时，剩余关联客户 (contact1) 自动解绑并不被物理删除
		wDelComp := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/companies/%d", companyID), nil)
		if wDelComp.Code != http.StatusOK {
			t.Fatalf("delete company expected 200, got %d: %s", wDelComp.Code, wDelComp.Body.String())
		}

		// 检查 contact1 依然存在且 company_id 安全置空
		wGetC1 := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d", contact1ID), nil)
		if wGetC1.Code != http.StatusOK {
			t.Fatalf("contact1 should still exist after company deletion, got %d", wGetC1.Code)
		}
		c1Fresh := getMap(wGetC1.Body.Bytes())
		if c1Fresh["company_id"] != nil {
			t.Fatalf("contact1 company_id should be nil after company deleted, got %v", c1Fresh["company_id"])
		}
	})
}
