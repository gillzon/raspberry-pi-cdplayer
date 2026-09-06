const {test}=require('node:test');
const assert=require('node:assert/strict');
const TrackNavigation=require('./navigation.js');

function setup(tracks=[1,2,3,4],current=0){
  let nextID=0,preview;
  const timers=new Map(),sent=[];
  const nav=new TrackNavigation((...args)=>sent.push(args),value=>preview=value,(callback,delay)=>{assert.equal(delay,250);timers.set(++nextID,callback);return nextID},id=>timers.delete(id));
  nav.update('disc-a',tracks,current);
  return {nav,sent,timers,preview:()=>preview,flush(){const callbacks=[...timers.values()];timers.clear();callbacks.forEach(fn=>fn())}};
}

test('rapid Next clicks issue one command for the final target',()=>{
  const s=setup();s.nav.step(1);s.nav.step(1);s.nav.step(1);
  assert.deepEqual(s.sent,[]);assert.equal(s.timers.size,1);assert.equal(s.preview(),4);
  s.flush();assert.deepEqual(s.sent,[[4,'disc-a']]);assert.equal(s.preview(),null);
});
test('status polling does not discard accumulated clicks',()=>{
  const s=setup([1,3,5,7]);s.nav.step(1);s.nav.update('disc-a',[1,3,5,7],0);s.nav.step(1);
  s.flush();assert.deepEqual(s.sent,[[5,'disc-a']]);
});
test('opposite clicks cancel each other and boundaries do not wrap',()=>{
  const s=setup();s.nav.step(1);s.nav.step(-1);s.flush();assert.deepEqual(s.sent,[]);
  s.nav.step(-1);s.flush();assert.deepEqual(s.sent,[]);
  s.nav.update('disc-a',[1,2,3,4],3);s.nav.step(1);s.flush();assert.deepEqual(s.sent,[]);
});
test('disc replacement and explicit controls cancel pending navigation',()=>{
  const s=setup();s.nav.step(1);s.nav.update('disc-b',[1,2],0);s.flush();assert.deepEqual(s.sent,[]);
  s.nav.step(1);s.nav.cancel();s.flush();assert.deepEqual(s.sent,[]);
});
test('Next from stopped with no current track selects the first audio track',()=>{
  const s=setup([2,3],-1);s.nav.step(1);s.flush();assert.deepEqual(s.sent,[[2,'disc-a']]);
});

test('browser timers retain their required global receiver',()=>{
  const fs=require('node:fs'),vm=require('node:vm');
  const context=vm.createContext({});
  vm.runInContext(`
    const timers=new Map();let nextID=0;
    function setTimeout(callback,delay){
      if(this!==globalThis)throw new TypeError('Illegal invocation');
      timers.set(++nextID,callback);return nextID;
    }
    function clearTimeout(id){
      if(this!==globalThis)throw new TypeError('Illegal invocation');
      timers.delete(id);
    }
  `,context);
  vm.runInContext(fs.readFileSync(__dirname+'/navigation.js','utf8'),context);
  vm.runInContext(`
    const sent=[];
    const nav=new TrackNavigation((track)=>sent.push(track),()=>{});
    nav.update('disc',[1,2,3,4],0);
    nav.step(1);nav.step(1);
    for(const callback of timers.values())callback();
    if(sent.length!==1||sent[0]!==3)throw Error('Track command was not sent');
    nav.step(1);nav.cancel();
  `,context);
});
