import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import { transformSync } from 'esbuild';

function load(path, overrides={}) {
 const module={exports:{}};
 runInNewContext(transformSync(readFileSync(new URL(path,import.meta.url),'utf8'),{loader:'ts',format:'cjs'}).code,{module,exports:module.exports,...overrides});
 return module.exports;
}
const {chartRange,chartPath,mergeHistory}=load('../src/ui/metrics-history.ts');
const sample=(timestamp,ram=19)=>({streamId:'test',sequence:timestamp,timestamp,cpu:[{name:'cpu0',value:ram}],ram,disks:[],fans:[],temperatures:[]});
test('timestamped history survives reconnects, deduplicates replay, and expires old samples',()=>{
 let h=[sample(100000),sample(102000)];
 h=mergeHistory(h,[sample(102000),sample(110000)]);
 assert.deepEqual(Array.from(h,s=>s.timestamp),[100000,102000,110000]);
 const path=chartPath(h.map(s=>({timestamp:s.timestamp,value:s.ram})),[18,20],110000);
 assert.equal((path.match(/M/g)||[]).length,2,'interruption must not be joined by a line');
 assert.match(path,/M50,/);assert.match(path,/M60,/);
 h=mergeHistory(h,[sample(170001)]);assert.equal(h.length,1);
});
test('small changes are magnified and missing readings break each curve',()=>{
 const range=chartRange([19.001,19.1],'%');assert.ok(range[1]-range[0]<.2);
 const path=chartPath([{timestamp:100000,value:1},{timestamp:102000,value:null},{timestamp:104000,value:2}], [0,3],104000);
 assert.equal((path.match(/M/g)||[]).length,2);
 const zero=chartRange([0,0],' RPM');assert.equal(zero[0],0);assert.ok(zero[1]>0);
});
function streamSetup({delayedClose=false}={}) {
 const timers=new Map();let id=0;
 class Socket {
  static instances=[];
  handlers={};closed=false;
  constructor(url,protocols){this.url=url;this.protocols=protocols;Socket.instances.push(this);}
  addEventListener(name,fn){this.handlers[name]=fn;}
  close(code=1000){
   if(code!==1000 && (code<3000 || code>4999)) throw new DOMException('Invalid close code','InvalidAccessError');
   this.closed=true;this.closeCode=code;
   if(!delayedClose)this.emitClose(code);
  }
  emitClose(code){this.handlers.close?.({code});}
  message(samples,sentAt=samples.at(-1).timestamp){this.handlers.message({data:JSON.stringify({type:'metrics',sentAt,samples})});}
 }
 const {MetricsStream}=load('../src/ui/services/metrics-stream.ts',{
  DOMException,WebSocket:Socket,window:{location:{protocol:'https:',host:'nas.test'},setTimeout(fn,ms){timers.set(++id,{fn,ms});return id;},clearTimeout(id){timers.delete(id);}},
  require:()=>({currentSynoToken:()=> 'secret',synoTokenSubprotocol:()=> 'diskshell.syno-token.encoded'}),
 });
 const received=[];let disconnected=0;
 const stream=new MetricsStream((samples,fresh)=>received.push({samples,fresh}),()=>disconnected++);
 return {stream,Socket,timers,received,get disconnected(){return disconnected;}};
}
test('metrics socket suspends without retries, resumes securely, and ignores obsolete callbacks',()=>{
 const s=streamSetup();s.stream.setActive(true);const first=s.Socket.instances[0];
 assert.equal(first.url,'wss://nas.test/diskshell/metrics');assert.ok(!first.url.includes('secret'));
 first.message([sample(100000)]);assert.equal(s.received.length,1);
 s.stream.setActive(false);assert.equal(first.closed,true);assert.equal(s.timers.size,0);
 s.stream.setActive(true);first.message([sample(102000)]);assert.equal(s.received.length,1);
 s.Socket.instances[1].message([sample(100000)],110000);assert.equal(s.received.at(-1).fresh,false);
 s.stream.dispose();assert.equal(s.timers.size,0);
});
test('disconnect retries with backoff but revoked sessions stop retrying',()=>{
 const s=streamSetup();s.stream.setActive(true);s.Socket.instances[0].emitClose(1006);
 let retry=[...s.timers.values()][0];assert.equal(retry.ms,1000);s.timers.clear();retry.fn();
 s.Socket.instances[1].emitClose(1006);retry=[...s.timers.values()][0];assert.equal(retry.ms,2000);s.timers.clear();retry.fn();
 s.Socket.instances[2].emitClose(1008);assert.equal(s.timers.size,0);s.stream.dispose();
});

test('stream restart replaces history even when server time moved backwards',()=>{
 const h=mergeHistory([sample(1000000,19)],[{...sample(900000,90),streamId:'restarted',sequence:1}]);
 assert.equal(h.length,1);assert.equal(h[0].ram,90);
 const replay=mergeHistory(h,[{...h[0]}]);assert.equal(replay.length,1);
});
test('temporary authentication failure reconnects',()=>{
 const s=streamSetup();s.stream.setActive(true);s.Socket.instances[0].emitClose(1013);
 const retry=[...s.timers.values()][0];assert.equal(retry.ms,1000);s.timers.clear();retry.fn();
 assert.equal(s.Socket.instances.length,2);s.stream.dispose();
});
test('details reposition on resize and close when their widget is hidden',()=>{
 const {systemMonitorComponent:c}=load('../src/ui/components/system-monitor.ts',{require:()=>({messages:{}})});
 let width=1040,shown=true;
 const button={getClientRects:()=>shown?[{}]:[],getBoundingClientRect:()=>({left:850,bottom:60})};
 const monitor={selected:'temperature',$el:{querySelector:()=>button,closest:()=>({getBoundingClientRect:()=>({left:0,top:0,width,height:600})})}};
 c.methods.positionDetails.call(monitor);assert.equal(monitor.left,744);
 width=950;c.methods.positionDetails.call(monitor);assert.equal(monitor.left,654);
 shown=false;c.methods.positionDetails.call(monitor);assert.equal(monitor.selected,'');
});

function fireTimer(s,ms) {
 const entry=[...s.timers.entries()].find(([,timer])=>timer.ms===ms);
 assert.ok(entry,`expected ${ms}ms timer`);
 s.timers.delete(entry[0]);entry[1].fn();
}
test('watchdog marks stale and reconnects before delayed close; late events cannot affect replacement',()=>{
 const s=streamSetup({delayedClose:true});s.stream.setActive(true);
 const first=s.Socket.instances[0];first.message([sample(100000)]);
 fireTimer(s,20000);
 assert.equal(s.disconnected,1);assert.equal(first.closeCode,4000);
 assert.equal(s.timers.size,1);fireTimer(s,1000);
 const second=s.Socket.instances[1];second.message([sample(102000)]);
 const received=s.received.length, disconnected=s.disconnected;
 first.message([sample(104000)]);first.emitClose(1008);first.handlers.error();
 assert.equal(s.received.length,received);assert.equal(s.disconnected,disconnected);
 assert.equal(s.timers.size,1,'only replacement watchdog remains');
 second.emitClose(1008);assert.equal(s.timers.size,0,'current revoked session still stops');
 s.stream.dispose();
});
test('malformed frames use a legal application close code and recover without close event',()=>{
 const s=streamSetup({delayedClose:true});s.stream.setActive(true);
 const first=s.Socket.instances[0];first.message([sample(100000)]);
 assert.doesNotThrow(()=>first.handlers.message({data:'invalid JSON'}));
 assert.equal(first.closeCode,4002);assert.equal(s.disconnected,1);
 fireTimer(s,1000);assert.equal(s.Socket.instances.length,2);
 s.Socket.instances[1].handlers.message({data:JSON.stringify({type:'unexpected'})});
 fireTimer(s,2000);assert.equal(s.Socket.instances.length,3);
 s.stream.dispose();assert.equal(s.timers.size,0);
});
test('deactivation cancels pending watchdog recovery and ignores late close',()=>{
 const s=streamSetup({delayedClose:true});s.stream.setActive(true);
 const first=s.Socket.instances[0];fireTimer(s,20000);
 s.stream.setActive(false);first.emitClose(1006);
 assert.equal(s.timers.size,0);assert.equal(s.Socket.instances.length,1);
 s.stream.setActive(true);s.stream.dispose();assert.equal(s.timers.size,0);
});
