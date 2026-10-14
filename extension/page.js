// Fixed top-frame operations only. This function is serialized by chrome.scripting.
export function pageOperation(command) {
  if (!['current', 'visible', 'fill', 'click'].includes(command.action)) return {ok: false};
  if (window !== window.top || !Number.isFinite(command.deadline_ms) || Date.now() >= command.deadline_ms) return {ok: false};
  if (!document.__xgatewayDocumentID) {
    Object.defineProperty(document, '__xgatewayDocumentID', {
      value: `${Date.now()}-${Math.random()}-${Math.random()}`
    });
  }
  const current = {url: location.href, id: document.__xgatewayDocumentID};
  if (command.action === 'current') return {ok: true, document: current};
  const element = document.querySelector(command.selector);
  if (!element || !element.getClientRects().length || element.disabled || getComputedStyle(element).visibility === 'hidden') return {ok: false};
  if (command.action === 'visible') return {ok: true};
  if (command.action === 'fill') {
    if (!command.document || current.url !== command.document.url || current.id !== command.document.id || element.readOnly) return {ok: false};
    if (!setValue(element, command.value)) return {ok: false};
    return {ok: true};
  }
  if (command.action === 'click') {
    if (!command.document || current.url !== command.document.url || current.id !== command.document.id) return {ok: false};
    element.click(); return {ok: true};
  }
  return {ok: false};
  function setValue(element, value) {
    let prototype;
    if (element instanceof HTMLInputElement) prototype = HTMLInputElement.prototype;
    else if (element instanceof HTMLTextAreaElement) prototype = HTMLTextAreaElement.prototype;
    else return false;
    Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, value);
    element.dispatchEvent(new Event('input', {bubbles: true}));
    element.dispatchEvent(new Event('change', {bubbles: true}));
    return true;
  }
}
