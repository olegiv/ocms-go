// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const script = fs.readFileSync(0, 'utf8');
function element() {
  return {children: [], textContent: '', value: '', style: {}, disabled: false,
    classList: {toggle() {}}, addEventListener(name, fn) { this[name] = fn; },
    appendChild(child) {child.parent = this; this.children.push(child);},
    remove() {this.parent.children.splice(this.parent.children.indexOf(this), 1);}, focus() {}};
}
function event(data, newline = '\n') {return 'data: ' + JSON.stringify(data) + newline + newline;}
async function run(chunks, failed = false, retry = false) {
  const elements = new Map(['toggle','window','close','messages','input','send'].map(id => ['dify-chat-'+id, element()]));
  let calls = 0, cancelled = 0, released = 0;
  const context = {TextDecoder, Uint8Array, console: {error() {}}, document: {
    getElementById(id) {return elements.get(id);}, createElement: element},
    fetch: async () => {
      const data = (calls++ === 0 ? chunks : [event({event:'message',answer:'Retry works'})]).map(c => typeof c === 'string' ? Buffer.from(c) : c);
      let index = 0;
      return {ok:true, body:{getReader:() => ({
        read:async () => {if(data[index] instanceof Error)throw data[index++]; return index < data.length ? {value:data[index++],done:false} : {done:true};},
        cancel:async () => {cancelled++;}, releaseLock() {released++;}})}};
    }};
  vm.runInNewContext(script, context);
  const input = elements.get('dify-chat-input'), send = elements.get('dify-chat-send'), messages = elements.get('dify-chat-messages');
  input.value = 'test'; await send.click();
  assert.equal(send.disabled, false);
  const errors = messages.children.filter(c => c.className === 'dify-err');
  assert.equal(errors.length, failed ? 1 : 0);
  assert.equal(messages.children.filter(c => c.className === 'dify-msg dify-msg-bot' && !c.textContent).length, 0);
  if(failed)assert.equal(errors[0].textContent, 'Failed to send message. Please try again.');
  else assert.equal(messages.children.find(c => c.className === 'dify-msg dify-msg-bot').textContent, 'Hello 世界');
  assert.equal(cancelled, 1); assert.equal(released, 1);
  if(retry) {input.value='retry'; await send.click(); assert.equal(calls,2); assert.equal(send.disabled,false);
    assert.equal(messages.children.at(-1).textContent,'Retry works');}
}
(async () => {
  const success = event({event:'message',answer:'Hello '}) + event({event:'agent_message',answer:'世界'}) + event({event:'message_end',message_id:'id'});
  await run([success]);
  const bytes = Buffer.from(success);
  await run([...bytes].map(b => Buffer.from([b])));
  await run([...Buffer.from(success.replaceAll('\n','\r\n'))].map(b => Buffer.from([b])));
  await run([success.trimEnd()]);
  await run([': ping\n\ndata: {"event":"message",\ndata: "answer":"Hello 世界"}\n\n']);
  await run([event({event:'error',message:'private upstream details'})],true,true);
  await run([event({event:'workflow_finished',data:{status:'failed',error:'private traceback'}})],true,true);
  await run([event({event:'workflow_finished',data:{status:'succeeded'}})],true);
  await run(['data: invalid\n\n'],true);
  await run([new Error('connection closed')],true,true);
  await run([event({event:'message',answer:'Hello 世界'})+event({event:'error'})],true);
  console.log('Dify streaming and retry tests passed');
})().catch(error => {console.error(error); process.exitCode=1;});
