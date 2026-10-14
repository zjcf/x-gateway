const reconnect = document.querySelector('#reconnect');
const status = document.querySelector('#status');
const permission = document.querySelector('#permission');

function render(result) {
  status.textContent = result?.connected ? '已自动连接，可用标签页：' + (result.tabs || 0) + '。' : '正在自动重连；请检查 xweb Native Messaging 安装。';
  permission.textContent = result?.permitted ? '已声明所有 HTTP/HTTPS 网站访问权限。' : '网站访问权限受限。请在 Chrome 扩展详情中将“网站访问权限”设为“在所有网站上”。';
}
status.textContent = '正在读取自动连接状态…';
chrome.runtime.sendMessage({type: 'status'}).then(render, () => render());
reconnect.addEventListener('click', async () => {
  reconnect.disabled = true;
  try {render(await chrome.runtime.sendMessage({type: 'reconnect'}));}
  catch {render();}
  finally {reconnect.disabled = false;}
});
