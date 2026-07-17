package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxBodyCapture = 128 * 1024 // por corpo (req/resp) guardado no inspetor
	keepRequests   = 100        // últimas N requisições
)

// capBuf guarda até max bytes (para o inspetor) mas finge consumir tudo, então
// serve como destino de um io.TeeReader sem limitar o que é encaminhado.
type capBuf struct {
	b     bytes.Buffer
	trunc bool
}

func (c *capBuf) Write(p []byte) (int, error) {
	if room := maxBodyCapture - c.b.Len(); room > 0 {
		if room >= len(p) {
			c.b.Write(p)
		} else {
			c.b.Write(p[:room])
			c.trunc = true
		}
	} else if len(p) > 0 {
		c.trunc = true
	}
	return len(p), nil
}

type reqRecord struct {
	ID          int         `json:"id"`
	At          string      `json:"at"`
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	Status      int         `json:"status"`
	DurationMs  int64       `json:"duration_ms"`
	ReqHeaders  http.Header `json:"req_headers"`
	ReqBody     string      `json:"req_body"`
	ReqTrunc    bool        `json:"req_trunc"`
	RespHeaders http.Header `json:"resp_headers"`
	RespBody    string      `json:"resp_body"`
	RespTrunc   bool        `json:"resp_trunc"`
}

type recorder struct {
	mu    sync.Mutex
	seq   int
	items []*reqRecord // mais recentes no fim
}

func newRecorder() *recorder { return &recorder{} }

func (r *recorder) add(rec *reqRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	rec.ID = r.seq
	r.items = append(r.items, rec)
	if len(r.items) > keepRequests {
		r.items = r.items[len(r.items)-keepRequests:]
	}
}

func (r *recorder) list() []*reqRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*reqRecord, len(r.items))
	for i, it := range r.items { // do mais novo para o mais velho
		out[len(r.items)-1-i] = it
	}
	return out
}

// startInspector sobe o painel local em 127.0.0.1:<port> (tenta as próximas
// portas se ocupada). Retorna a URL real, ou "" se não conseguiu.
func startInspector(r *recorder, port int) string {
	var ln net.Listener
	var err error
	for p := port; p < port+10; p++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			port = p
			break
		}
	}
	if ln == nil {
		return ""
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(r.list())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, inspectorHTML)
	})
	go http.Serve(ln, mux)
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// serveHTTPTunnel substitui o pipe cru: entende HTTP para registrar cada
// requisição no inspetor, encaminhando corpo/streaming sem alterar. WebSocket
// (Upgrade) cai para pipe cru — sem inspeção, mas funciona igual.
func serveHTTPTunnel(stream net.Conn, localAddr string, r *recorder) {
	defer stream.Close()
	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		io.WriteString(stream, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer local.Close()
	sr := bufio.NewReader(stream)
	lr := bufio.NewReader(local)

	for {
		req, err := http.ReadRequest(sr)
		if err != nil {
			return
		}
		if strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") ||
			req.Header.Get("Upgrade") != "" {
			// WebSocket/upgrade: encaminha e vira túnel cru bidirecional.
			req.Write(local)
			go io.Copy(local, sr)
			io.Copy(stream, lr)
			return
		}

		var reqBuf, respBuf capBuf
		start := time.Now()
		if req.Body != nil {
			req.Body = io.NopCloser(io.TeeReader(req.Body, &reqBuf))
		}
		urlStr := req.URL.RequestURI()
		method := req.Method
		reqHdr := req.Header.Clone()
		if err := req.Write(local); err != nil {
			return
		}
		resp, err := http.ReadResponse(lr, req)
		if err != nil {
			return
		}
		resp.Body = io.NopCloser(io.TeeReader(resp.Body, &respBuf))
		status := resp.StatusCode
		respHdr := resp.Header.Clone()
		writeErr := resp.Write(stream) // encaminha (streaming preservado)
		r.add(&reqRecord{
			At:          time.Now().Format("15:04:05"),
			Method:      method,
			URL:         urlStr,
			Status:      status,
			DurationMs:  time.Since(start).Milliseconds(),
			ReqHeaders:  reqHdr,
			ReqBody:     reqBuf.b.String(),
			ReqTrunc:    reqBuf.trunc,
			RespHeaders: respHdr,
			RespBody:    respBuf.b.String(),
			RespTrunc:   respBuf.trunc,
		})
		if writeErr != nil || req.Close || resp.Close {
			return
		}
	}
}

const inspectorHTML = `<!doctype html><html lang="pt-br"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>lgrok · inspetor</title>
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{margin:0;font:14px/1.5 system-ui,sans-serif;background:#0f1115;color:#e6e6e6;height:100vh;display:flex;flex-direction:column}
header{padding:10px 16px;border-bottom:1px solid #262b36;font-weight:600}
header small{color:#8a93a2;font-weight:400}
main{flex:1;display:flex;min-height:0}
#list{width:340px;border-right:1px solid #262b36;overflow:auto}
#detail{flex:1;overflow:auto;padding:16px}
.row{padding:8px 12px;border-bottom:1px solid #1c212b;cursor:pointer;display:flex;gap:8px;align-items:center}
.row:hover{background:#161a22}.row.sel{background:#1e2530}
.m{font-weight:700;font-size:12px;min-width:52px}
.s{margin-left:auto;font-variant-numeric:tabular-nums;font-size:12px}
.p{color:#cdd3dc;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.ok{color:#5ad17f}.warn{color:#e6c84a}.err{color:#ff8a8a}.get{color:#6ea8fe}.post{color:#a78bfa}
h3{margin:18px 0 6px;font-size:13px;color:#8a93a2;text-transform:uppercase;letter-spacing:.04em;display:flex;align-items:center;gap:8px}
pre{background:#161a22;border:1px solid #262b36;border-radius:8px;padding:10px;overflow:auto;white-space:pre-wrap;word-break:break-word;margin:0}
button{font:inherit;font-size:12px;padding:2px 8px;border-radius:6px;border:1px solid #333a47;background:#232833;color:#e6e6e6;cursor:pointer}
button:hover{background:#2c323f}
.empty{color:#8a93a2;padding:24px;text-align:center}
.trunc{color:#e6c84a;font-size:12px;margin-top:4px}
</style></head><body>
<header>lgrok · inspetor de requisições <small>— atualiza sozinho · 127.0.0.1</small></header>
<main>
<div id="list"><div class="empty">aguardando requisições…</div></div>
<div id="detail"><div class="empty">selecione uma requisição</div></div>
</main>
<script>
let items=[],sel=null;
const cls=s=>s>=500?'err':s>=400?'warn':'ok';
const mcls=m=>m==='GET'?'get':m==='POST'?'post':'';
function esc(s){return (s||'').replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]))}
function hdrs(h){return Object.entries(h||{}).map(([k,v])=>k+': '+v.join(', ')).join('\n')}
function copyBtn(text){const b=document.createElement('button');b.textContent='copiar';b.onclick=()=>navigator.clipboard.writeText(text);return b}
function renderList(){
  const el=document.getElementById('list');
  if(!items.length){el.innerHTML='<div class="empty">aguardando requisições…</div>';return}
  el.innerHTML='';
  for(const it of items){
    const d=document.createElement('div');d.className='row'+(sel===it.id?' sel':'');
    d.innerHTML='<span class="m '+mcls(it.method)+'">'+it.method+'</span><span class="p">'+esc(it.url)+'</span><span class="s '+cls(it.status)+'">'+it.status+'</span>';
    d.onclick=()=>{sel=it.id;renderList();renderDetail()};el.appendChild(d);
  }
}
function section(title,body,trunc){
  const h=document.createElement('h3');h.textContent=title;h.appendChild(copyBtn(body));
  const pre=document.createElement('pre');pre.textContent=body||'(vazio)';
  const frag=document.createDocumentFragment();frag.appendChild(h);frag.appendChild(pre);
  if(trunc){const t=document.createElement('div');t.className='trunc';t.textContent='⚠ corpo truncado (limite do inspetor)';frag.appendChild(t)}
  return frag;
}
function renderDetail(){
  const el=document.getElementById('detail');const it=items.find(x=>x.id===sel);
  if(!it){el.innerHTML='<div class="empty">selecione uma requisição</div>';return}
  el.innerHTML='';
  const t=document.createElement('div');t.innerHTML='<b>'+it.method+'</b> '+esc(it.url)+' · <span class="'+cls(it.status)+'">'+it.status+'</span> · '+it.duration_ms+'ms · '+it.at;
  el.appendChild(t);
  el.appendChild(section('Request headers',hdrs(it.req_headers)));
  el.appendChild(section('Request body',it.req_body,it.req_trunc));
  el.appendChild(section('Response headers',hdrs(it.resp_headers)));
  el.appendChild(section('Response body',it.resp_body,it.resp_trunc));
}
async function tick(){
  try{items=await (await fetch('/api/requests')).json()}catch(e){}
  renderList();if(sel)renderDetail();
}
tick();setInterval(tick,1500);
</script></body></html>`
