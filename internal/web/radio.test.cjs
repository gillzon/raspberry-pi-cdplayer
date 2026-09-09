const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const scriptFrom=path=>[...fs.readFileSync(path,'utf8').matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n');

for(const page of ['display','index'])test(page+' shows live radio and changes stations without CD track navigation',async()=>{
 const elements=new Map(),posts=[];
 const element=()=>({dataset:{},hidden:false,disabled:false,textContent:'',setAttribute(){},removeAttribute(){},replaceChildren(){}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 const controls=['play','pause','stop','next','previous','eject'].map(action=>Object.assign(element(),{dataset:{action}}));
 let state={source:'radio',radio:{id:'p1',name:'P1'},disc:{ID:'old-cd',Tracks:[1,2]},metadata:{album:'Old CD',cover_url:'old-cover'},mpd:{state:'play',song:'0'},updated:new Date().toISOString()};
 const context=vm.createContext({
  document:{getElementById:get,querySelector:selector=>controls.find(b=>selector.includes(b.dataset.action))||controls[0],querySelectorAll:selector=>selector==='[data-action]'||selector==='[data-action],[data-track]'?controls:[],createElement:element,addEventListener(){}},
  window:{parent:{postMessage(){}}},
  TrackNavigation:class{update(){}cancel(){}step(){throw Error('Radio used CD navigation')}},
  fetch:async(url,options)=>{if(options?.method==='POST')posts.push(JSON.parse(options.body));return {ok:true,json:async()=>url==='/api/radio'?[{id:'p1',name:'P1'}]:state}},
  AbortSignal,setTimeout:()=>{},Date,
 });
 vm.runInContext(scriptFrom(__dirname+'/'+page+'.html'),context);
 await vm.runInContext('refresh()',context);
 assert.equal(get(page==='display'?'title':'albumTitle').textContent,'P1');
 assert.equal(get(page==='display'?'songTime':'time').textContent,'Live');
 if(page==='display'){
  assert.equal(get('next').disabled,false);
  assert.equal(get('pause').textContent,'Stop');
  await vm.runInContext("press('next')",context);
 }else{
  assert.equal(get('tracks').hidden,true);
  controls.find(b=>b.dataset.action==='next').onclick();
 }
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(posts.at(-1).action,'next');
 state={...state,mpd:{state:'stop'},error:'Stream disconnected'};
 await vm.runInContext('refresh()',context);
 assert.match(get(page==='display'?'message':'error').textContent,/Stream disconnected/);
});
