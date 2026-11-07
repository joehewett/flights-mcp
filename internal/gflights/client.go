// Package gflights fetches and decodes Google Flights result pages.
//
// There is no API key. The client requests the public results page the
// browser would load and reads the data the page embeds for its own
// script. That data changes shape without notice, so parse errors are
// reported as such rather than hidden.
package gflights

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	searchURL = "https://www.google.com/travel/flights"
	// tfu selects the result tabs. It is constant in page-issued URLs.
	resultTabs = "EgQIABABIgA"
	userAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	// A consent cookie Google's own pages set after "accept"; it skips the
	// EU consent interstitial.
	socsCookie = "CAESHAgBEhJnd3NfMjAyMzA4MTAtMF9SQzIaAmVuIAEaBgiA_LyaBg"
)

// Errors the caller can act on.
var (
	ErrBlocked  = errors.New("google flights blocked the request (captcha or unusual traffic)")
	ErrConsent  = errors.New("google flights returned a consent page")
	ErrRejected = errors.New("google flights rejected the query")
)

// Client fetches result pages.
type Client struct {
	HTTP     *http.Client
	Language string // hl, default en
	Country  string // gl, optional
}

// New returns a client with sane timeouts.
func New() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 40 * time.Second},
		Language: "en",
	}
}

// SearchURL is the public page for a tfs value. It doubles as the link a
// person opens to see and book the same results.
func (c *Client) SearchURL(tfs, currency string) string {
	q := url.Values{}
	q.Set("tfs", tfs)
	q.Set("hl", c.lang())
	q.Set("curr", currency)
	q.Set("tfu", resultTabs)
	if c.Country != "" {
		q.Set("gl", c.Country)
	}
	return searchURL + "?" + q.Encode()
}

func (c *Client) lang() string {
	if c.Language == "" {
		return "en"
	}
	return c.Language
}

// Search fetches and parses one results page.
func (c *Client) Search(ctx context.Context, tfs, currency string) (*Results, error) {
	html, err := c.fetch(ctx, c.SearchURL(tfs, currency))
	if err != nil {
		return nil, err
	}
	data, err := ExtractDS1(html)
	if err != nil {
		return nil, err
	}
	return Parse(data, currency)
}

func (c *Client) fetch(ctx context.Context, u string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(500*(attempt)) * time.Millisecond):
			}
		}
		body, retry, err := c.once(ctx, u)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) once(ctx context.Context, u string) (body []byte, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", c.lang()+",en;q=0.9")
	req.AddCookie(&http.Cookie{Name: "SOCS", Value: socsCookie})
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "PENDING+987"})
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, true, err
	}
	final := resp.Request.URL.String()
	switch {
	case strings.Contains(final, "consent.google"):
		return nil, false, ErrConsent
	case strings.Contains(final, "/sorry/"), strings.Contains(string(body), "unusual traffic from your computer"):
		return nil, false, ErrBlocked
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, true, fmt.Errorf("%w: HTTP 429", ErrBlocked)
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("google flights HTTP %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("google flights HTTP %d", resp.StatusCode)
	}
	return body, false, nil
}
