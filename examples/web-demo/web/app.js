'use strict';
const $ = id => document.getElementById(id);
const time = iso => new Date(iso).toLocaleTimeString('ko-KR',{timeZone:'Asia/Seoul',hour12:false});
async function request(path, options={}) {
  const controller=new AbortController(); const timer=setTimeout(()=>controller.abort(),8000);
  try { const r=await fetch(path,{...options,cache:'no-store',signal:controller.signal}); if(!r.ok)throw new Error(await r.text()); return await r.json(); }
  finally { clearTimeout(timer); }
}
async function refresh() {
  try { const start=performance.now(); const data=await request('/api/status'); $('badge').textContent='● 연결 정상'; $('badge').className='badge good'; $('latency').textContent=Math.round(performance.now()-start)+' ms'; $('clock').textContent=time(data.serverTime); $('address').textContent=location.host; $('checked').textContent='마지막 확인 '+time(data.serverTime)+' · 요청 '+data.requests+'회'; }
  catch { $('badge').textContent='연결 확인 필요'; $('badge').className='badge bad'; $('checked').textContent='응답이 없습니다. 서버나 터널 연결을 확인하세요.'; }
}
$('echo-form').addEventListener('submit',async event=>{
  event.preventDefault(); $('send').disabled=true; $('reply').textContent='터널을 통해 서버로 전송 중…';
  try { const start=performance.now(); const data=await request('/api/echo',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:$('message').value})}); $('reply').textContent='서버가 받은 메시지\n'+data.message+'\n\n왕복 '+Math.round(performance.now()-start)+' ms · 서버 시각 '+time(data.serverTime); }
  catch(e) { $('reply').textContent='전송 실패: '+e.message; }
  finally { $('send').disabled=false; }
});
refresh(); setInterval(()=>{if(!document.hidden)refresh();},5000);
