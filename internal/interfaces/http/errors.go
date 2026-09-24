package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"aigc-platform/internal/pkg/apperr"
	"aigc-platform/internal/pkg/logger"
)

// ErrorSpec describes one API error code. Clients localize by code and fill
// the text from the response's params; message is English detail for logs
// and API users, never shown to end users as is.
type ErrorSpec struct {
	Status int    `json:"status"`
	Doc    string `json:"doc"`
}

// ErrorCatalog is every code the API returns. A test keeps it in step with
// the code base; the frontend's i18n check reads it (cli error-codes).
var ErrorCatalog = map[string]ErrorSpec{
	// Generic.
	"bad_request":   {http.StatusBadRequest, "malformed request body or parameters"},
	"unauthorized":  {http.StatusUnauthorized, "missing or expired sign-in"},
	"forbidden":     {http.StatusForbidden, "not allowed for this account"},
	"not_found":     {http.StatusNotFound, "the resource does not exist or is not yours"},
	"not_supported": {http.StatusUnprocessableEntity, "the operation does not apply to this resource"},
	"rate_limited":  {http.StatusTooManyRequests, "too many requests; retry later"},
	"internal":      {http.StatusInternalServerError, "server error; the message names the failed step only"},
	"unavailable":   {http.StatusServiceUnavailable, "a backing service is unavailable"},

	// Auth and account.
	"invalid_credentials":   {http.StatusUnauthorized, "wrong email/phone or password"},
	"invalid_token":         {http.StatusUnauthorized, "refresh or reset token invalid or expired"},
	"invalid_code":          {http.StatusUnauthorized, "verification code wrong or expired"},
	"too_many_attempts":     {http.StatusTooManyRequests, "too many code or sign-in attempts"},
	"email_taken":           {http.StatusConflict, "the email is already registered"},
	"account_suspended":     {http.StatusForbidden, "the account is deactivated"},
	"sms_not_configured":    {http.StatusServiceUnavailable, "phone sign-in is not available"},
	"sms_send_failed":       {http.StatusBadGateway, "the SMS could not be sent"},
	"email_not_configured":  {http.StatusServiceUnavailable, "email codes are not available"},
	"email_send_failed":     {http.StatusBadGateway, "the email could not be sent"},
	"google_not_configured": {http.StatusServiceUnavailable, "Google sign-in is not available"},
	"trial_used":            {http.StatusConflict, "the guest trial was already used"},
	"self_deactivate":       {http.StatusConflict, "admins cannot deactivate themselves"},
	"last_admin":            {http.StatusConflict, "the last admin cannot be demoted"},

	// Jobs: submission and quotes. params carry limits and allowed values.
	"submit_failed":                 {http.StatusUnprocessableEntity, "the job could not be created; no credits were held"},
	"estimate_failed":               {http.StatusUnprocessableEntity, "the request cannot be priced"},
	"unknown_workflow":              {http.StatusUnprocessableEntity, "unknown workflow_name"},
	"insufficient_credits":          {http.StatusPaymentRequired, "the balance does not cover the reservation"},
	"price_changed":                 {http.StatusConflict, "the quote is stale; params.credits_total is the current price"},
	"openai_not_enabled":            {http.StatusForbidden, "OpenAI image generation is not enabled for this account"},
	"provider_unavailable":          {http.StatusServiceUnavailable, "the provider is not configured here (params.provider)"},
	"provider_workflow_unsupported": {http.StatusUnprocessableEntity, "the provider does not offer this workflow (params.provider)"},
	"provider_option_unsupported":   {http.StatusUnprocessableEntity, "the option applies to another provider"},
	"image_count_exceeded":          {http.StatusUnprocessableEntity, "too many images (params.max)"},
	"image_size_unsupported":        {http.StatusUnprocessableEntity, "size not offered (params.allowed)"},
	"image_quality_unsupported":     {http.StatusUnprocessableEntity, "quality not offered (params.allowed)"},
	"resolution_invalid":            {http.StatusUnprocessableEntity, "video resolution not offered (params.allowed)"},
	"video_refs_exclusive":          {http.StatusUnprocessableEntity, "first/last frames cannot be combined with reference media"},
	"video_ratio_required":          {http.StatusUnprocessableEntity, "text-to-video needs a fixed ratio"},
	"shots_required":                {http.StatusUnprocessableEntity, "at least one shot is required"},
	"shot_refs_invalid":             {http.StatusUnprocessableEntity, "a shot references itself or a later shot (params.shot)"},
	"comic_panel_count":             {http.StatusUnprocessableEntity, "panel count out of range (params.min, params.max)"},
	"story_split_failed":            {http.StatusBadGateway, "the story could not be split into panels; nothing was charged"},
	"invalid_comic":                 {http.StatusUnprocessableEntity, "comic request or document is invalid"},
	"text_length":                   {http.StatusUnprocessableEntity, "text too long or empty (params.field, params.max)"},
	"references_too_many":           {http.StatusUnprocessableEntity, "too many reference images (params.max)"},
	"reference_unavailable":         {http.StatusUnprocessableEntity, "a reference image is missing, deleted or not yours"},
	"reference_format":              {http.StatusUnprocessableEntity, "reference images must be PNG, JPEG or WebP"},
	"reference_too_large":           {http.StatusUnprocessableEntity, "a reference image is too large (params.max_mb)"},
	"reference_incomplete":          {http.StatusUnprocessableEntity, "a reference image has not finished uploading"},
	"invalid_project":               {http.StatusUnprocessableEntity, "project name or id invalid"},

	// Jobs: lifecycle.
	"invalid_title":           {http.StatusUnprocessableEntity, "title length out of range (params.max)"},
	"invalid_job":             {http.StatusUnprocessableEntity, "job reference invalid"},
	"job_active":              {http.StatusConflict, "the job is still running; cancel it first (params.status)"},
	"job_finished":            {http.StatusConflict, "the job has already finished"},
	"not_awaiting_review":     {http.StatusConflict, "the job is not waiting for a preview decision"},
	"review_decision_invalid": {http.StatusUnprocessableEntity, "review decision names an unknown shot or conflicts (params.shot)"},
	"node_not_failed":         {http.StatusConflict, "only failed steps can be retried"},
	"resume_failed":           {http.StatusUnprocessableEntity, "the review decision could not be applied"},
	"cancel_failed":           {http.StatusUnprocessableEntity, "the job could not be cancelled"},
	"delete_failed":           {http.StatusUnprocessableEntity, "the job could not be deleted"},
	"retry_failed":            {http.StatusUnprocessableEntity, "the step could not be retried"},

	// Assets, uploads, comics, generation helpers.
	"invalid_asset":     {http.StatusUnprocessableEntity, "asset reference invalid"},
	"upload_missing":    {http.StatusUnprocessableEntity, "the upload has not reached storage"},
	"upload_too_large":  {http.StatusRequestEntityTooLarge, "the file exceeds the size limit"},
	"mime_mismatch":     {http.StatusUnprocessableEntity, "the file content does not match its type"},
	"version_conflict":  {http.StatusConflict, "the document changed elsewhere; params or body carry the server version"},
	"generation_failed": {http.StatusBadGateway, "a helper generation (rewrite, planning) failed"},
	"upstream_error":    {http.StatusBadGateway, "the provider returned an error"},
}

// writeError answers with err's code when it carries one; otherwise with
// fallbackCode, hiding the detail of server errors.
func writeError(c *gin.Context, err error, fallbackStatus int, fallbackCode string) {
	if e, ok := apperr.As(err); ok {
		status := http.StatusUnprocessableEntity
		if spec, known := ErrorCatalog[e.Code]; known {
			status = spec.Status
		}
		body := errBody(e.Code, e.Message)
		if len(e.Params) > 0 {
			body["params"] = e.Params
		}
		c.JSON(status, body)
		return
	}
	message := err.Error()
	if fallbackStatus >= http.StatusInternalServerError {
		logger.L().Error("request failed", zap.String("path", c.FullPath()), zap.Error(err))
		message = fallbackCode
	}
	c.JSON(fallbackStatus, errBody(fallbackCode, message))
}
