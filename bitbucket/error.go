package bitbucket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DrFaust92/bitbucket-go-client"
	"golang.org/x/oauth2"
)

type ClientScopeError struct {
	Error struct {
		Message string `json:"message"`
		Detail  struct {
			Required []string `json:"required"`
			Granted  []string `json:"granted"`
		} `json:"detail"`
	} `json:"error"`
}

func handleClientError(httpResponse *http.Response, err error) error {
	if oauthError, ok := err.(*oauth2.RetrieveError); ok {
		return fmt.Errorf("%s: %s", oauthError.Response.Status, oauthError.ErrorDescription)
	}

	if httpResponse == nil || httpResponse.StatusCode < 400 {
		return nil
	}

	// A rate limited response carries a text/plain body that says nothing
	// about how long to wait, so the headers are what make it actionable.
	if hint := rateLimitHint(httpResponse); hint != "" {
		return fmt.Errorf("%s: %s", httpResponse.Status, hint)
	}

	clientHttpError, ok := err.(bitbucket.GenericSwaggerError)
	if ok {
		errorBody := extractErrorMessage(clientHttpError.Body())
		return fmt.Errorf("%s: %s", httpResponse.Status, errorBody)
	}

	if err != nil {
		return err
	}

	return nil
}

func extractErrorMessage(body []byte) string {
	var bitbucketHttpError bitbucket.ModelError
	if err := json.Unmarshal(body, &bitbucketHttpError); err == nil {
		return bitbucketHttpError.Error_.Message
	}

	var clientScopeErr ClientScopeError
	if err := json.Unmarshal(body, &clientScopeErr); err == nil {
		message := clientScopeErr.Error.Message
		required := clientScopeErr.Error.Detail.Required
		granted := clientScopeErr.Error.Detail.Granted
		return fmt.Sprintf("%s Required: %v Granted: %v", message, required, granted)
	}

	return string(body[:])
}

// rateLimitHint turns a 429 response into a message an operator can act on.
// Bitbucket answers with "Rate limit for this resource has been exceeded" and
// puts the only useful detail, the time until the window resets, in a header.
func rateLimitHint(resp *http.Response) string {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}

	msg := "rate limit exceeded"

	if d, ok := resetDelay(resp.Header, time.Now()); ok {
		msg += fmt.Sprintf(", resets in %ds", int(d.Round(time.Second).Seconds()))

		if limit := resp.Header.Get("X-RateLimit-Limit"); limit != "" {
			msg += fmt.Sprintf(" (limit %s)", limit)
		}
	}

	return msg + "; reduce -parallelism or re-run after the window resets"
}
