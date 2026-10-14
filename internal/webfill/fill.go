package webfill

import (
	"context"
	"time"
	"x-gateway/internal/localstore"
	"x-gateway/internal/model"
)

type Target struct {
	ID        string `json:"target_id"`
	Origin    string `json:"origin"`
	Transport string `json:"transport"`
	ProfileID string `json:"profile_id,omitempty"`
	BridgeID  string `json:"bridge_id,omitempty"`
	TabID     int    `json:"tab_id,omitempty"`
}
type Options struct {
	TargetID         string `json:"target_id"`
	Origin           string `json:"origin"`
	UsernameSelector string `json:"username_selector,omitempty"`
	PasswordSelector string `json:"password_selector,omitempty"`
	SubmitSelector   string `json:"submit_selector,omitempty"`
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
}
type Result struct {
	TargetID  string `json:"target_id"`
	Filled    bool   `json:"filled"`
	Submitted bool   `json:"submitted"`
}
type Document struct {
	URL string `json:"url"`
	ID  string `json:"id"`
}
type driver interface {
	current(context.Context) (Document, error)
	visible(context.Context, string) error
	fill(context.Context, string, string, Document) error
	click(context.Context, string, Document) error
}

func Origin(raw string) (string, error) { return localstore.WebOrigin(raw) }
func validate(o Options) error {
	origin, e := Origin(o.Origin)
	if e != nil || origin != o.Origin {
		return model.Err("INVALID_ARGUMENT", "需要精确网站origin")
	}
	if o.UsernameSelector == "" && o.PasswordSelector == "" {
		return model.Err("INVALID_ARGUMENT", "至少指定一个用户名或密码selector")
	}
	if o.UsernameSelector != "" && o.UsernameSelector == o.PasswordSelector {
		return model.Err("INVALID_ARGUMENT", "用户名和密码不能使用相同selector")
	}
	for _, s := range []string{o.UsernameSelector, o.PasswordSelector, o.SubmitSelector} {
		if len(s) > 8192 {
			return model.Err("INVALID_ARGUMENT", "selector过长")
		}
	}
	return nil
}
func run(ctx context.Context, d driver, o Options) (Result, error) {
	result := Result{TargetID: o.TargetID}
	if e := validate(o); e != nil {
		return result, e
	}
	doc, e := d.current(ctx)
	if e != nil {
		return result, e
	}
	origin, e := Origin(doc.URL)
	if e != nil || origin != o.Origin || doc.ID == "" {
		return result, model.Err("ORIGIN_MISMATCH", "当前页面与凭据登记网站不一致")
	}
	// Preflight requested credential fields before mutation. Legacy Chrome
	// querySelector semantics intentionally select the first match.
	for _, s := range []string{o.UsernameSelector, o.PasswordSelector} {
		if s != "" {
			if e = d.visible(ctx, s); e != nil {
				return result, e
			}
		}
	}
	if o.UsernameSelector != "" {
		if e = d.fill(ctx, o.UsernameSelector, o.Username, doc); e != nil {
			return result, e
		}
	}
	if o.PasswordSelector != "" {
		if e = d.fill(ctx, o.PasswordSelector, o.Password, doc); e != nil {
			return result, e
		}
	}
	result.Filled = true
	if o.SubmitSelector != "" {
		// Forms often enable their button only after input/change rendering.
		ready, cancel := context.WithTimeout(ctx, time.Second)
		for {
			e = d.visible(ready, o.SubmitSelector)
			if e == nil {
				break
			}
			select {
			case <-ready.Done():
				cancel()
				return result, model.Err("PAGE_OPERATION_FAILED", "填写完成，但提交按钮不可用；未点击")
			case <-time.After(25 * time.Millisecond):
			}
		}
		cancel()
		if e = d.click(ctx, o.SubmitSelector, doc); e != nil {
			return result, e
		}
		result.Submitted = true
	}
	return result, nil
}
