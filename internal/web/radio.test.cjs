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

test('display Shuffle starts a USB mix from every source and shows progress while waiting',async()=>{
 const elements=new Map(),posts=[];
 const get=id=>{if(!elements.has(id))elements.set(id,{disabled:false,setAttribute(){},removeAttribute(){}});return elements.get(id)};
 let state,finish;
 const context=vm.createContext({
  document:{getElementById:get,addEventListener(){}},window:{parent:{postMessage(){}}},
  TrackNavigation:class{update(){}cancel(){}step(){throw Error('Shuffle navigated CD tracks')}},
  fetch:async(url,options)=>{
   if(options?.method==='POST'){
    posts.push(JSON.parse(options.body));
    await new Promise(resolve=>finish=resolve);
    state={source:'usb',usb_mix:true,usb:{title:'Shuffled MP3'},mpd:{state:'play'},updated:new Date().toISOString()};
   }
   return {ok:true,json:async()=>state};
  },AbortSignal,Date,setTimeout(){},
 });
 for(const source of ['cd','usb','radio','spotify','idle']){
  state={source,disc:{Tracks:[]},updated:new Date().toISOString()};
  if(posts.length===0)vm.runInContext(scriptFrom(__dirname+'/display.html'),context);
  await vm.runInContext('refresh()',context);
  assert.equal(get('shuffle').disabled,false);
  get('shuffle').onclick();
  assert.equal(posts.at(-1).action,'usb-mix');
  assert.match(get('message').textContent,/Preparing USB shuffle/);
  assert.equal(get('shuffle').disabled,true);
  finish();await new Promise(resolve=>setImmediate(resolve));
  assert.equal(get('title').textContent,'Shuffled MP3');
  assert.match(get('message').textContent,/shuffled/);
 }
});

test('display follows CD, station changes and USB without leaking CD text or artwork',async()=>{
 const elements=new Map();
 const get=id=>{if(!elements.has(id))elements.set(id,{setAttribute(){},removeAttribute(name){delete this[name]}});return elements.get(id)};
 let state={source:'cd',disc:{Tracks:[]},updated:new Date().toISOString()};
 let reloads=0;
 const context=vm.createContext({
  document:{getElementById:get,addEventListener(){}},window:{parent:{postMessage(){}},location:{reload(){reloads++}}},
  TrackNavigation:class{update(){}cancel(){}},
  fetch:async()=>({ok:true,json:async()=>state}),AbortSignal,Date,setTimeout(){},
 });
 vm.runInContext(scriptFrom(__dirname+'/display.html'),context);
 await vm.runInContext('refresh()',context);
 assert.equal(get('source').textContent,'CD');
 assert.equal(get('title').textContent,'Insert a CD');
 for(const station of ['P1','P2','P3','Rockklassiker']){
  state={...state,source:'radio',radio:{name:station},metadata:{album:'Old CD',cover_url:'old.jpg',message:'Insert a CD'},audio:{error:'Insert a CD'},selection:{pending:true,error:'Old CD error'},mpd:{state:'play'}};
  await vm.runInContext('refresh()',context);
  assert.equal(get('source').textContent,'Radio · '+station);
  assert.equal(get('title').textContent,station);
  assert.equal(get('status').textContent,'Playing');
  assert.equal(get('album').textContent,'Live broadcast');
  assert.equal(get('placeholder').textContent,'RADIO');
  assert.doesNotMatch(get('message').textContent,/CD/);
 }
 state={...state,source:'usb',usb:{title:'An MP3',artist:'Artist',album:'Album',cover_url:'usb.jpg'}};
 await vm.runInContext('refresh()',context);
 assert.equal(get('source').textContent,'USB Music');
 assert.equal(get('title').textContent,'An MP3');
 assert.equal(get('artist').textContent,'Artist');
 assert.equal(get('album').textContent,'Album');
 assert.equal(get('cover').src,'usb.jpg');
 state={...state,source:'radio',radio:{name:'P1'}};
 await vm.runInContext('refresh()',context);
 assert.equal(get('cover').src,undefined);
 assert.equal(get('cover').hidden,true);
 assert.equal(reloads,0);
 state={...state,display_version:'new-release'};
 await vm.runInContext('refresh()',context);
 assert.equal(reloads,1);
});
