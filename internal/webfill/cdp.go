package webfill

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"time"
	"x-gateway/internal/model"
)

func endpoint(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "ws") {
		return model.Err("BROWSER_UNAVAILABLE", "CDP需为loopback HTTP或WebSocket")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return model.Err("BROWSER_UNAVAILABLE", "CDP需为loopback地址")
	}
	return nil
}
func cdpTargets(ctx context.Context, c *cdpClient) ([]Target, error) {
	var list struct {
		Targets []struct {
			Type string `json:"type"`
			ID   string `json:"targetId"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if e := c.call(ctx, "Target.getTargets", nil, "", &list); e != nil {
		return nil, e
	}
	out := []Target{}
	for _, page := range list.Targets {
		if page.Type != "page" {
			continue
		}
		origin, e := Origin(page.URL)
		if e == nil {
			out = append(out, Target{ID: page.ID, Origin: origin, Transport: "cdp"})
		}
	}
	return out, nil
}
func CDPTargets(ctx context.Context, ep string) ([]Target, error) {
	c, e := connectCDP(ctx, ep)
	if e != nil {
		return nil, e
	}
	defer c.Close()
	return cdpTargets(ctx, c)
}
func CDPFill(ctx context.Context, ep string, o Options) (Result, error) {
	result := Result{TargetID: o.TargetID}
	if e := validate(o); e != nil {
		return result, e
	}
	c, e := connectCDP(ctx, ep)
	if e != nil {
		return result, e
	}
	defer c.Close()
	targets, e := cdpTargets(ctx, c)
	if e != nil {
		return result, e
	}
	found := ""
	for _, target := range targets {
		if o.TargetID != "" && target.ID != o.TargetID {
			continue
		}
		if target.Origin != o.Origin {
			if target.ID == o.TargetID {
				return result, model.Err("ORIGIN_MISMATCH", "所选CDP页面与凭据网站不一致")
			}
			continue
		}
		if found != "" {
			return result, model.Err("TARGET_AMBIGUOUS", "多个页面符合网站，请明确target-id")
		}
		found = target.ID
	}
	if found == "" {
		return result, model.Err("TARGET_NOT_FOUND", "没有符合网站的CDP页面")
	}
	o.TargetID = found
	var attached struct {
		ID string `json:"sessionId"`
	}
	if e = c.call(ctx, "Target.attachToTarget", map[string]any{"targetId": found, "flatten": true}, "", &attached); e != nil {
		return result, e
	}
	defer func() {
		child, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.call(child, "Target.detachFromTarget", map[string]any{"sessionId": attached.ID}, "", nil)
	}()
	return run(ctx, &cdpDriver{client: c, session: attached.ID}, o)
}

type cdpDriver struct {
	client  *cdpClient
	session string
}

func (d *cdpDriver) evaluate(ctx context.Context, expression string, out any) error {
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	e := d.client.call(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true}, d.session, &r)
	if e != nil {
		return e
	}
	if len(r.Exception) > 0 || json.Unmarshal(r.Result.Value, out) != nil {
		return model.Err("PAGE_OPERATION_FAILED", "指定selector不可用或页面已变化")
	}
	return nil
}
func js(value any) string { b, _ := json.Marshal(value); return string(b) }
func (d *cdpDriver) current(ctx context.Context) (Document, error) {
	var doc Document
	e := d.evaluate(ctx, `(()=>{if(!document.__xgatewayDocumentID)Object.defineProperty(document,'__xgatewayDocumentID',{value:Date.now()+'-'+Math.random()+'-'+Math.random()});return {url:location.href,id:document.__xgatewayDocumentID}})()`, &doc)
	return doc, e
}
func (d *cdpDriver) visible(ctx context.Context, s string) error {
	var ok bool
	e := d.evaluate(ctx, fmt.Sprintf(`(()=>{const e=document.querySelector(%s);return !!e&&!!e.getClientRects().length&&!e.disabled&&getComputedStyle(e).visibility!=='hidden'})()`, js(s)), &ok)
	if e != nil {
		return e
	}
	if !ok {
		return model.Err("PAGE_OPERATION_FAILED", "指定selector不可见或已禁用")
	}
	return nil
}
func (d *cdpDriver) fill(ctx context.Context, s, value string, doc Document) error {
	var ok bool
	expr := fmt.Sprintf(`(()=>{if(window!==top||location.href!==%s||document.__xgatewayDocumentID!==%s)return false;const e=document.querySelector(%s);if(!e||!(e instanceof HTMLInputElement||e instanceof HTMLTextAreaElement)||e.disabled||e.readOnly||!e.getClientRects().length||getComputedStyle(e).visibility==='hidden')return false;const p=e instanceof HTMLInputElement?HTMLInputElement.prototype:HTMLTextAreaElement.prototype;Object.getOwnPropertyDescriptor(p,'value').set.call(e,%s);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`, js(doc.URL), js(doc.ID), js(s), js(value))
	e := d.evaluate(ctx, expr, &ok)
	if e != nil {
		return e
	}
	if !ok {
		return model.Err("PAGE_OPERATION_FAILED", "页面或输入框已变化")
	}
	return nil
}
func (d *cdpDriver) click(ctx context.Context, s string, doc Document) error {
	var ok bool
	expr := fmt.Sprintf(`(()=>{if(window!==top||location.href!==%s||document.__xgatewayDocumentID!==%s)return false;const e=document.querySelector(%s);if(!e||e.disabled||!e.getClientRects().length||getComputedStyle(e).visibility==='hidden')return false;e.click();return true})()`, js(doc.URL), js(doc.ID), js(s))
	e := d.evaluate(ctx, expr, &ok)
	if e != nil {
		return e
	}
	if !ok {
		return model.Err("PAGE_OPERATION_FAILED", "页面或按钮已变化")
	}
	return nil
}
