package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/logger"
)

type Result struct {
	OK     bool           
	Status int            
	Body   map[string]any 
	Err    string         
}

type Client struct {
	http    *http.Client
	version string
}

func New(version string) *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}, version: version}
}

func (c *Client) HTTP() *http.Client { return c.http }

func (c *Client) Get(path string) Result            { return c.do(http.MethodGet, path, nil) }
func (c *Client) Post(path string, body any) Result { return c.do(http.MethodPost, path, body) }
func (c *Client) Put(path string, body any) Result  { return c.do(http.MethodPut, path, body) }

func (c *Client) do(method, path string, body any) Result {
	base, err := url.Parse(config.Get().ServerURL())
	if err != nil || base.Host == "" {
		return Result{Err: "Некорректный адрес сервера"}
	}
	base.Path = path
	base.RawQuery = ""

	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return Result{Err: "Не удалось сериализовать запрос"}
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base.String(), payload)
	if err != nil {
		return Result{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Autolectures/"+c.version)
	if tok := config.Get().AccessToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		r := Result{Err: fmt.Sprintf("Сервер недоступен: %v", friendlyNetErr(err))}
		logger.Warnf("API", "%s → %s", path, r.Err)
		return r
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	r := Result{Status: resp.StatusCode, Body: map[string]any{}}
	var parsed any
	if json.Unmarshal(data, &parsed) == nil {
		switch v := parsed.(type) {
		case map[string]any:
			r.Body = v
		case []any:
			r.Body["items"] = v
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		r.OK = true
		return r
	}
	if d, ok := r.Body["detail"].(string); ok && d != "" {
		r.Err = d
	} else {
		r.Err = fmt.Sprintf("Ошибка сервера (HTTP %d)", resp.StatusCode)
	}
	logger.Warnf("API", "%s → %s", path, r.Err)
	return r
}

func friendlyNetErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

