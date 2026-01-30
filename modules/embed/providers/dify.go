// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package providers

import (
	"fmt"
	"html/template"
	"strings"
)

// DifyProvider implements the Provider interface for Dify AI chat.
type DifyProvider struct{}

// NewDify creates a new Dify provider instance.
func NewDify() *DifyProvider {
	return &DifyProvider{}
}

// ID returns the provider identifier.
func (p *DifyProvider) ID() string {
	return "dify"
}

// Name returns the display name.
func (p *DifyProvider) Name() string {
	return "Dify AI Chat"
}

// Description returns the provider description.
func (p *DifyProvider) Description() string {
	return "Embed Dify AI chatbot widget on your website"
}

// SettingsSchema returns the configuration fields for Dify.
func (p *DifyProvider) SettingsSchema() []SettingField {
	return []SettingField{
		{
			ID:          "api_endpoint",
			Name:        "API Endpoint",
			Description: "Dify API endpoint URL (e.g., https://api.dify.ai/v1 or your self-hosted URL)",
			Type:        "url",
			Required:    true,
			Default:     "https://api.dify.ai/v1",
			Placeholder: "https://api.dify.ai/v1",
		},
		{
			ID:          "api_key",
			Name:        "API Key",
			Description: "Your Dify application API key (starts with 'app-')",
			Type:        "text",
			Required:    true,
			Placeholder: "app-xxxxxxxxxxxxxxxx",
		},
		{
			ID:          "bot_name",
			Name:        "Bot Name",
			Description: "Display name for the chatbot",
			Type:        "text",
			Default:     "AI Assistant",
			Placeholder: "AI Assistant",
		},
		{
			ID:          "welcome_message",
			Name:        "Welcome Message",
			Description: "Initial message shown when chat opens",
			Type:        "text",
			Default:     "Hello! How can I help you today?",
			Placeholder: "Hello! How can I help you today?",
		},
		{
			ID:          "placeholder_text",
			Name:        "Input Placeholder",
			Description: "Placeholder text for the message input",
			Type:        "text",
			Default:     "Type your message...",
			Placeholder: "Type your message...",
		},
		{
			ID:          "primary_color",
			Name:        "Primary Color",
			Description: "Main color for the chat widget",
			Type:        "color",
			Default:     "#1C64F2",
			Placeholder: "#1C64F2",
		},
		{
			ID:          "position",
			Name:        "Position",
			Description: "Chat widget position on the page",
			Type:        "select",
			Default:     "bottom-right",
			Options: []SelectOption{
				{Value: "bottom-right", Label: "Bottom Right"},
				{Value: "bottom-left", Label: "Bottom Left"},
			},
		},
		{
			ID:          "opener_questions",
			Name:        "Opener Questions",
			Description: "Suggested questions shown when chat opens (one per line)",
			Type:        "textarea",
			Placeholder: "What can you help with?\nTell me about pricing\nHow do I get started?",
		},
		{
			ID:          "show_suggested",
			Name:        "Show Suggested Questions",
			Description: "Display AI-suggested follow-up questions after each response",
			Type:        "checkbox",
			Default:     "",
		},
	}
}

// Validate validates the Dify settings.
func (p *DifyProvider) Validate(settings map[string]string) error {
	apiEndpoint := strings.TrimSpace(settings["api_endpoint"])
	apiKey := strings.TrimSpace(settings["api_key"])

	if apiEndpoint == "" {
		return fmt.Errorf("API endpoint is required")
	}
	if !strings.HasPrefix(apiEndpoint, "http://") && !strings.HasPrefix(apiEndpoint, "https://") {
		return fmt.Errorf("API endpoint must start with http:// or https://")
	}
	if apiKey == "" {
		return fmt.Errorf("API key is required")
	}

	return nil
}

// RenderHead returns HTML for the <head> section.
func (p *DifyProvider) RenderHead(_ map[string]string) template.HTML {
	return ""
}

// RenderBody returns the custom Dify chat widget.
func (p *DifyProvider) RenderBody(settings map[string]string) template.HTML {
	apiEndpoint := strings.TrimSuffix(strings.TrimSpace(settings["api_endpoint"]), "/")
	apiKey := strings.TrimSpace(settings["api_key"])

	if apiEndpoint == "" || apiKey == "" {
		return ""
	}

	botName := strings.TrimSpace(settings["bot_name"])
	if botName == "" {
		botName = "AI Assistant"
	}

	welcomeMessage := strings.TrimSpace(settings["welcome_message"])
	if welcomeMessage == "" {
		welcomeMessage = "Hello! How can I help you today?"
	}

	placeholderText := strings.TrimSpace(settings["placeholder_text"])
	if placeholderText == "" {
		placeholderText = "Type your message..."
	}

	primaryColor := strings.TrimSpace(settings["primary_color"])
	if primaryColor == "" {
		primaryColor = "#1C64F2"
	}

	position := strings.TrimSpace(settings["position"])
	if position == "" {
		position = "bottom-right"
	}

	positionCSS := "right: 20px;"
	if position == "bottom-left" {
		positionCSS = "left: 20px;"
	}

	// Parse opener questions (one per line)
	openerQuestions := strings.TrimSpace(settings["opener_questions"])
	var openerJS string
	if openerQuestions != "" {
		lines := strings.Split(openerQuestions, "\n")
		var escaped []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				escaped = append(escaped, "'"+template.JSEscapeString(line)+"'")
			}
		}
		if len(escaped) > 0 {
			openerJS = "[" + strings.Join(escaped, ",") + "]"
		}
	}
	if openerJS == "" {
		openerJS = "[]"
	}

	// Parse show_suggested setting (default: disabled)
	showSuggested := settings["show_suggested"] == "1"
	showSuggestedJS := "false"
	if showSuggested {
		showSuggestedJS = "true"
	}

	// Escape values for safe JS/HTML output
	safeAPIEndpoint := template.JSEscapeString(apiEndpoint)
	safeAPIKey := template.JSEscapeString(apiKey)
	safeWelcomeMessage := template.JSEscapeString(welcomeMessage)
	safePrimaryColor := template.HTMLEscapeString(primaryColor)

	widget := fmt.Sprintf(`<!-- Dify AI Chat Widget -->
<div id="dify-chat-widget">
  <button id="dify-chat-toggle" aria-label="Open chat">
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
    </svg>
  </button>
  <div id="dify-chat-window" class="dify-chat-hidden">
    <div id="dify-chat-header">
      <div id="dify-chat-title">
        <div id="dify-chat-avatar">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/>
            <path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/>
          </svg>
        </div>
        <span>%s</span>
      </div>
      <button id="dify-chat-close" aria-label="Close chat">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M18 6 6 18"/><path d="m6 6 12 12"/>
        </svg>
      </button>
    </div>
    <div id="dify-chat-messages"></div>
    <div id="dify-chat-input-area">
      <textarea id="dify-chat-input" placeholder="%s" rows="1"></textarea>
      <button id="dify-chat-send" aria-label="Send message">
        <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/>
        </svg>
      </button>
    </div>
  </div>
</div>
<style>
#dify-chat-widget{position:fixed;bottom:20px;%sz-index:9999;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif}
#dify-chat-toggle{width:56px;height:56px;border-radius:50%%;background:%s;color:#fff;border:none;cursor:pointer;display:flex;align-items:center;justify-content:center;box-shadow:0 4px 12px rgba(0,0,0,.15);transition:transform .2s,box-shadow .2s}
#dify-chat-toggle:hover{transform:scale(1.05);box-shadow:0 6px 16px rgba(0,0,0,.2)}
#dify-chat-window{position:absolute;bottom:70px;%swidth:380px;height:520px;background:#fff;border-radius:16px;box-shadow:0 8px 32px rgba(0,0,0,.15);display:flex;flex-direction:column;overflow:hidden;transition:opacity .2s,transform .2s}
#dify-chat-window.dify-chat-hidden{opacity:0;transform:translateY(10px);pointer-events:none}
#dify-chat-header{background:%s;color:#fff;padding:16px;display:flex;align-items:center;justify-content:space-between}
#dify-chat-title{display:flex;align-items:center;gap:10px;font-weight:600;font-size:15px}
#dify-chat-avatar{width:32px;height:32px;background:rgba(255,255,255,.2);border-radius:50%%;display:flex;align-items:center;justify-content:center}
#dify-chat-close{background:none;border:none;color:#fff;cursor:pointer;padding:4px;opacity:.8;transition:opacity .2s}
#dify-chat-close:hover{opacity:1}
#dify-chat-messages{flex:1;overflow-y:auto;padding:16px;display:flex;flex-direction:column;gap:12px}
.dify-msg{max-width:85%%;padding:12px 16px;border-radius:16px;font-size:14px;line-height:1.5;word-wrap:break-word}
.dify-msg-user{align-self:flex-end;background:%s;color:#fff;border-bottom-right-radius:4px}
.dify-msg-bot{align-self:flex-start;background:#f3f4f6;color:#1f2937;border-bottom-left-radius:4px}
.dify-typing{display:flex;gap:4px;padding:12px 16px}
.dify-typing span{width:8px;height:8px;background:#9ca3af;border-radius:50%%;animation:dify-bounce 1.4s infinite ease-in-out}
.dify-typing span:nth-child(1){animation-delay:-.32s}
.dify-typing span:nth-child(2){animation-delay:-.16s}
@keyframes dify-bounce{0%%,80%%,100%%{transform:scale(0)}40%%{transform:scale(1)}}
#dify-chat-input-area{padding:12px 16px;border-top:1px solid #e5e7eb;display:flex;gap:8px;align-items:flex-end}
#dify-chat-input{flex:1;border:1px solid #d1d5db;border-radius:12px;padding:10px 14px;font-size:14px;resize:none;max-height:120px;font-family:inherit;line-height:1.4}
#dify-chat-input:focus{outline:none;border-color:%s;box-shadow:0 0 0 3px %s33}
#dify-chat-send{width:40px;height:40px;background:%s;color:#fff;border:none;border-radius:50%%;cursor:pointer;display:flex;align-items:center;justify-content:center;transition:background .2s;flex-shrink:0}
#dify-chat-send:hover{filter:brightness(1.1)}
#dify-chat-send:disabled{background:#9ca3af;cursor:not-allowed}
.dify-err{color:#dc2626;font-size:13px;padding:8px 12px;background:#fef2f2;border-radius:8px}
.dify-openers{display:flex;flex-direction:column;gap:8px;margin-top:12px}
.dify-opener{background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:10px 14px;font-size:13px;text-align:left;cursor:pointer;transition:all .2s;color:#374151}
.dify-opener:hover{border-color:%s;background:#f9fafb}
@media(max-width:480px){#dify-chat-window{width:calc(100vw - 40px);height:calc(100vh - 100px);bottom:70px;right:20px;left:20px}}
</style>
<script>
(function(){
var API='%s',KEY='%s',WELCOME='%s',OPENERS=%s,SHOW_SUGGESTED=%s;
var convId=null,isOpen=false,busy=false,userId='user-'+Math.random().toString(36).substr(2,9),openersShown=false,lastMsgId=null;
var tog=document.getElementById('dify-chat-toggle');
var win=document.getElementById('dify-chat-window');
var cls=document.getElementById('dify-chat-close');
var msgs=document.getElementById('dify-chat-messages');
var inp=document.getElementById('dify-chat-input');
var send=document.getElementById('dify-chat-send');

function toggle(){
  isOpen=!isOpen;
  win.classList.toggle('dify-chat-hidden',!isOpen);
  if(isOpen&&msgs.children.length===0){
    addMsg(WELCOME,'bot');
    if(OPENERS.length>0&&!openersShown){showOpeners();openersShown=true;}
  }
  if(isOpen)inp.focus();
}

function showOpeners(){
  var c=document.createElement('div');
  c.className='dify-openers';
  c.id='dify-openers';
  for(var i=0;i<OPENERS.length;i++){
    var b=document.createElement('button');
    b.className='dify-opener';
    b.textContent=OPENERS[i];
    b.onclick=(function(q){return function(){hideOpeners();inp.value=q;doSend();};})(OPENERS[i]);
    c.appendChild(b);
  }
  msgs.appendChild(c);
  msgs.scrollTop=msgs.scrollHeight;
}

function hideOpeners(){var e=document.getElementById('dify-openers');if(e)e.remove();}

function showSuggestions(arr){
  hideSuggestions();
  var c=document.createElement('div');
  c.className='dify-openers';
  c.id='dify-suggestions';
  for(var i=0;i<arr.length;i++){
    var b=document.createElement('button');
    b.className='dify-opener';
    b.textContent=arr[i];
    b.onclick=(function(q){return function(){inp.value=q;doSend();};})(arr[i]);
    c.appendChild(b);
  }
  msgs.appendChild(c);
  msgs.scrollTop=msgs.scrollHeight;
}

function hideSuggestions(){var e=document.getElementById('dify-suggestions');if(e)e.remove();}

function addMsg(t,type){
  var d=document.createElement('div');
  d.className='dify-msg dify-msg-'+type;
  d.innerHTML=type==='bot'?fmt(t):esc(t);
  msgs.appendChild(d);
  msgs.scrollTop=msgs.scrollHeight;
  return d;
}

function esc(s){return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}

function fmt(t){
  return t.replace(/\*\*(.+?)\*\*/g,'<strong>$1</strong>')
    .replace(/\*(.+?)\*/g,'<em>$1</em>')
    .replace(/\n/g,'<br>');
}

function showTyp(){
  var d=document.createElement('div');
  d.className='dify-msg dify-msg-bot dify-typing';
  d.id='dify-typ';
  d.innerHTML='<span></span><span></span><span></span>';
  msgs.appendChild(d);
  msgs.scrollTop=msgs.scrollHeight;
}

function hideTyp(){var e=document.getElementById('dify-typ');if(e)e.remove();}

async function doSend(){
  var t=inp.value.trim();
  if(!t||busy)return;
  hideOpeners();
  hideSuggestions();
  addMsg(t,'user');
  inp.value='';
  inp.style.height='auto';
  busy=true;
  send.disabled=true;
  showTyp();
  try{
    var r=await fetch(API+'/chat-messages',{
      method:'POST',
      headers:{'Authorization':'Bearer '+KEY,'Content-Type':'application/json'},
      body:JSON.stringify({inputs:{},query:t,response_mode:'streaming',conversation_id:convId||'',user:userId})
    });
    if(!r.ok)throw new Error('API error: '+r.status);
    hideTyp();
    var botMsg=addMsg('','bot');
    var full='',msgId=null;
    var reader=r.body.getReader();
    var dec=new TextDecoder();
    while(true){
      var chunk=await reader.read();
      if(chunk.done)break;
      var lines=dec.decode(chunk.value).split('\n');
      for(var i=0;i<lines.length;i++){
        var ln=lines[i];
        if(ln.indexOf('data: ')===0){
          try{
            var d=JSON.parse(ln.slice(6));
            if(d.event==='message'||d.event==='agent_message'){
              full+=(d.answer||'');
              botMsg.innerHTML=fmt(full);
              msgs.scrollTop=msgs.scrollHeight;
              if(d.message_id)msgId=d.message_id;
            }
            if(d.event==='message_end'&&d.message_id)msgId=d.message_id;
            if(d.conversation_id)convId=d.conversation_id;
          }catch(e){}
        }
      }
    }
    if(SHOW_SUGGESTED&&msgId){
      try{
        var sr=await fetch(API+'/messages/'+msgId+'/suggested?user='+userId,{headers:{'Authorization':'Bearer '+KEY}});
        if(sr.ok){var sd=await sr.json();if(sd.data&&sd.data.length)showSuggestions(sd.data);}
      }catch(e){}
    }
  }catch(e){
    hideTyp();
    var err=document.createElement('div');
    err.className='dify-err';
    err.textContent='Failed to send message. Please try again.';
    msgs.appendChild(err);
    console.error('Dify error:',e);
  }finally{busy=false;send.disabled=false;}
}

tog.addEventListener('click',toggle);
cls.addEventListener('click',toggle);
send.addEventListener('click',doSend);
inp.addEventListener('keydown',function(e){if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();doSend();}});
inp.addEventListener('input',function(){this.style.height='auto';this.style.height=Math.min(this.scrollHeight,120)+'px';});
})();
</script>
<!-- End Dify AI Chat Widget -->`,
		template.HTMLEscapeString(botName),
		template.HTMLEscapeString(placeholderText),
		positionCSS,
		safePrimaryColor,
		positionCSS,
		safePrimaryColor,
		safePrimaryColor,
		safePrimaryColor, safePrimaryColor,
		safePrimaryColor,
		safePrimaryColor, // opener button hover border
		safeAPIEndpoint,
		safeAPIKey,
		safeWelcomeMessage,
		openerJS,
		showSuggestedJS,
	)

	return template.HTML(widget)
}
