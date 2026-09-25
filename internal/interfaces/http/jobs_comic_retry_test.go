package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"aigc-platform/internal/application/jobsvc"
	"aigc-platform/internal/domain/workflow"
)

// partialComic creates a four-panel classic comic whose first two panels
// finished and whose third failed (the fourth, chained to it, never ran).
func partialComic(t *testing.T, s *Server, eng *testEngine, token string) string {
	t.Helper()
	rec := doJSON(t, s.Router(), http.MethodPost, "/api/v1/jobs", createJobRequest{WorkflowName: "image.comic4", Spec: jobsvc.Spec{
		Panels: []string{"a cat wakes up", "the cat eats", "the cat naps in the sun", "the cat dreams"},
	}}, token)
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	bizID, _ := created["biz_id"].(string)
	if bizID == "" {
		t.Fatalf("create comic: %d %s", rec.Code, rec.Body.String())
	}
	set := func(name, status, outputs string) {
		if _, err := eng.db.Exec(`UPDATE job_nodes n JOIN jobs j ON j.id = n.job_id SET n.status = ?, n.outputs_json = ? WHERE j.biz_id = ? AND n.node_name = ?`, status, outputs, bizID, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"enhance-panel-1", "enhance-panel-2", "enhance-panel-3"} {
		set(n, workflow.NodeSucceeded, `{"enhanced-prompt":"x"}`)
	}
	set("panel-1", workflow.NodeSucceeded, `{"asset-id":"img-1"}`)
	set("panel-2", workflow.NodeSucceeded, `{"asset-id":"img-2"}`)
	set("panel-3", workflow.NodeFailed, `null`)
	if _, err := eng.db.Exec(`UPDATE jobs SET status = 'partial', finished_at = NOW(3) WHERE biz_id = ?`, bizID); err != nil {
		t.Fatal(err)
	}
	return bizID
}

func TestComicPanelRetry(t *testing.T) {
	s, eng := newFullTestServer(t)
	token, _ := registerAndFund(t, s, 5000)
	bizID := partialComic(t, s, eng, token)
	r := s.Router()

	detail := doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+bizID, nil, token)
	var job map[string]any
	_ = json.Unmarshal(detail.Body.Bytes(), &job)
	if job["panel_retry"] != true {
		t.Fatalf("a partial classic comic should offer panel retry: %v", job["panel_retry"])
	}

	rec := doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/panels/retry/quote", map[string]any{}, token)
	var quote jobsvc.ComicRetryQuote
	if err := json.Unmarshal(rec.Body.Bytes(), &quote); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("quote: %d %s", rec.Code, rec.Body.String())
	}
	if len(quote.Panels) != 2 || quote.Panels[0].Index != 3 || quote.Panels[0].Text != "the cat naps in the sun" || quote.Panels[1].Index != 4 || quote.CreditsTotal <= 0 {
		t.Fatalf("quote = %+v", quote)
	}

	stale := quote.CreditsTotal + 1
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/panels/retry", map[string]any{"quote_total": stale}, token)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "price_changed") {
		t.Fatalf("stale quote: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/panels/retry", map[string]any{"texts": map[string]string{"1": "x"}}, token)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a finished panel cannot be retried: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+bizID+"/panels/retry", map[string]any{
		"texts": map[string]string{"3": "the cat naps on a windowsill"}, "quote_total": quote.CreditsTotal,
	}, token)
	var retried map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &retried)
	newID, _ := retried["biz_id"].(string)
	if rec.Code != http.StatusOK || newID == "" {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
	}

	var held int
	if err := eng.db.QueryRow(`SELECT credit_held FROM jobs WHERE biz_id = ?`, newID).Scan(&held); err != nil || held != quote.CreditsTotal {
		t.Fatalf("the retry must reserve exactly the quote: %d (%v), want %d", held, err, quote.CreditsTotal)
	}
	rows, err := eng.db.Query(`SELECT n.node_name, n.inputs_json FROM job_nodes n JOIN jobs j ON j.id = n.job_id WHERE j.biz_id = ? ORDER BY n.id`, newID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	inputs := map[string]map[string]workflow.Input{}
	var names []string
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			t.Fatal(err)
		}
		m := map[string]workflow.Input{}
		_ = json.Unmarshal(raw, &m)
		inputs[name], names = m, append(names, name)
	}
	if strings.Join(names, ",") != "enhance-panel-3,panel-3,enhance-panel-4,panel-4,compose" {
		t.Fatalf("retry nodes = %v", names)
	}
	var ref string
	_ = json.Unmarshal(inputs["panel-3"]["source-image-asset-id"].Value, &ref)
	if ref != "img-2" {
		t.Fatalf("panel 3 must continue from panel 2's image, got %q", ref)
	}
	if r := inputs["panel-4"]["source-image-asset-id"].Ref; r == nil || r.Node != "panel-3" {
		t.Fatalf("panel 4 must continue from the redrawn panel 3: %+v", inputs["panel-4"]["source-image-asset-id"])
	}
	var enhance string
	_ = json.Unmarshal(inputs["enhance-panel-3"]["prompt"].Value, &enhance)
	if !strings.Contains(enhance, "the cat naps on a windowsill") || strings.Contains(enhance, "naps in the sun") {
		t.Fatalf("panel 3 must use the edited text: %q", enhance)
	}
	var enhance4 string
	_ = json.Unmarshal(inputs["enhance-panel-4"]["prompt"].Value, &enhance4)
	if !strings.Contains(enhance4, "the cat naps on a windowsill") {
		t.Fatalf("panel 4's outline must follow the edit: %q", enhance4)
	}
	list := inputs["compose"]["asset-ids"].List
	var first string
	_ = json.Unmarshal(list[0].Value, &first)
	if len(list) != 4 || first != "img-1" || list[3].Ref == nil || list[3].Ref.Node != "panel-4" {
		t.Fatalf("compose = %+v", list)
	}
	newDetail := doJSON(t, r, http.MethodGet, "/api/v1/jobs/"+newID, nil, token)
	_ = json.Unmarshal(newDetail.Body.Bytes(), &job)
	if job["retry_of_job_id"] != bizID {
		t.Fatalf("retry_of_job_id = %v", job["retry_of_job_id"])
	}

	// Only classic comics with unfinished panels can do this.
	other := createJob(t, s, token)
	rec = doJSON(t, r, http.MethodPost, "/api/v1/jobs/"+other+"/panels/retry/quote", map[string]any{}, token)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "not_supported") {
		t.Fatalf("image job: %d %s", rec.Code, rec.Body.String())
	}
}
