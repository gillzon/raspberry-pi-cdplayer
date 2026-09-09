const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');

const scriptFrom=path=>[...fs.readFileSync(path,'utf8').matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n');

test('boot logo survives unavailable server and ignores unrelated readiness messages',()=>{
  let listener,retry,cleared=false,shown=false,focused=false,loads=0;
  const frame={contentWindow:{},removeAttribute(){},focus(){focused=true},set src(value){assert.equal(value,'http://localhost:8080/display');loads++}};
  const context=vm.createContext({
    document:{getElementById:()=>frame,body:{classList:{add(name){assert.equal(name,'ready');shown=true}}}},
    window:{addEventListener(type,callback){assert.equal(type,'message');listener=callback}},
    setInterval(callback){retry=callback;return 42},clearInterval(id){assert.equal(id,42);cleared=true},
  });
  vm.runInContext(scriptFrom(__dirname+'/../../deploy/boot/start.html'),context);
  assert.equal(loads,1);
  retry();assert.equal(loads,2);assert.equal(shown,false);
  const message={origin:'http://localhost:8080',source:frame.contentWindow,data:'cdplayer:display-ready'};
  listener({...message,origin:'http://elsewhere:8080'});
  listener({...message,source:{}});
  listener({...message,data:'other'});
  assert.equal(shown,false);
  listener(message);
  assert.equal(shown,true);assert.equal(cleared,true);assert.equal(focused,true);
  retry();assert.equal(loads,2);
});

test('display announces readiness once, only after a successful status render',async()=>{
  let available=false;
  const messages=[],elements=new Map();
  const get=id=>{if(!elements.has(id))elements.set(id,{setAttribute(){},removeAttribute(){}});return elements.get(id)};
  const context=vm.createContext({
    document:{getElementById:get,addEventListener(){}},
    window:{parent:{postMessage(token,origin){messages.push({token,origin,title:get('title').textContent})}}},
    TrackNavigation:class{update(){}cancel(){}},
    fetch:async()=>({ok:available,json:async()=>({source:'idle',updated:new Date().toISOString()})}),
    setTimeout(){},AbortSignal,Date,
  });
  vm.runInContext(scriptFrom(__dirname+'/display.html'),context);
  await vm.runInContext('refresh()',context);
  assert.equal(messages.length,0);
  available=true;
  await vm.runInContext('refresh()',context);
  assert.deepEqual(messages,[{token:'cdplayer:display-ready',origin:'*',title:'Press Play for CD'}]);
  await vm.runInContext('refresh()',context);
  assert.equal(messages.length,1);
});
