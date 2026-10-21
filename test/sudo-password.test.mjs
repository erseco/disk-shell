import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {transformSync} from 'esbuild';
import test from 'node:test';
function setup() {
 class Socket {
  static OPEN=1; static instances=[]; readyState=1; handlers={}; sent=[];
  constructor(url){this.url=url;Socket.instances.push(this)}
  addEventListener(name,fn){this.handlers[name]=fn}
  send(data){this.sent.push(JSON.parse(data))}
  close(){}
 }
 const module={exports:{}};
 runInNewContext(transformSync(readFileSync('src/ui/services/terminal-socket.ts','utf8'),{loader:'ts',format:'cjs'}).code,{
  module,exports:module.exports,WebSocket:Socket,document:{scripts:[]},window:{location:{protocol:'https:',host:'nas.test'}},require:()=>({messages:{}})
 });
 const options={name:'Sudo',sudo:true,password:'test-secret'};
 const events={onOpen(){},onClose(){},onOutput(){},onSession(){},onError(){}};
 return {socket:new module.exports.TerminalSocket(options,events),options,Socket};
}
test('sudo credential is sent once in open frame, cleared, and never sent as terminal input',()=>{
 const s=setup();s.socket.connect();const ws=s.Socket.instances[0];ws.handlers.open();
 assert.equal(ws.sent[0].type,'open');assert.equal(ws.sent[0].password,'test-secret');
 assert.equal(s.options.password,undefined);assert.ok(!ws.url.includes('test-secret'));
 s.socket.disconnect();s.socket.connect();const second=s.Socket.instances[1];
 ws.handlers.open();assert.equal(second.sent.length,0,'obsolete callbacks cannot send credentials');
 second.handlers.open();assert.equal(second.sent[0].password,undefined);s.socket.disconnect();
});
test('cancelling a pending connection clears its password before late open',()=>{
 const s=setup();s.socket.connect();const ws=s.Socket.instances[0];s.socket.disconnect();
 ws.handlers.open();assert.equal(ws.sent.length,0);assert.equal(s.options.password,undefined);
});
