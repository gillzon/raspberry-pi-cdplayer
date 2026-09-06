const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');

test('Spotify metadata renders and disconnect enables manual CD play without a command',async()=>{
 const elements=new Map();
 const element=()=>({dataset:{},hidden:false,disabled:false,textContent:'',setAttribute(){},removeAttribute(){},replaceChildren(){}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 const controls=['play','pause','stop','next','previous','eject'].map(action=>Object.assign(element(),{dataset:{action}}));
 const switchCD=get('switchCD');switchCD.dataset.action='source-cd';controls.push(switchCD);
 let state={source:'spotify',updated:new Date().toISOString(),device:'/dev/sr0',disc:{ID:'a',Tracks:[]},mpd:{},spotify:{enabled:true,running:true,playback:'playing',now:{title:'A Song',artist:'An Artist',album:'An Album',cover:'https://i.scdn.co/image/example'}}};
 let posts=0;
 const context=vm.createContext({
  document:{getElementById:get,querySelector:()=>controls[0],querySelectorAll:selector=>selector==='[data-action]'||selector==='[data-action],[data-track]'?controls:[],createElement:element},
  TrackNavigation:class{update(){}cancel(){}},
  fetch:async(url,options)=>{if(options?.method==='POST')posts++;return {ok:true,json:async()=>url==='/api/system'?{updated:new Date().toISOString()}:state}},
  AbortSignal,setTimeout:()=>{},Date,
 });
 const html=fs.readFileSync(__dirname+'/index.html','utf8');
 const script=[...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n');
 vm.runInContext(script,context);
 await vm.runInContext('refresh()',context);
 assert.equal(get('albumTitle').textContent,'An Album');
 assert.equal(get('albumArtist').textContent,'An Artist');
 assert.equal(get('now').textContent,'Playing · A Song');
 assert.equal(get('coverArt').src,'https://i.scdn.co/image/example');
 assert.equal(controls[0].disabled,true);
 state={...state,source:'idle',spotify:{...state.spotify,playback:'disconnected',now:{}}};
 await vm.runInContext('refresh()',context);
 assert.equal(controls[0].disabled,false);
 assert.equal(controls[0].textContent,'Play CD');
 assert.equal(switchCD.disabled,false);
 assert.equal(switchCD.textContent,'Play CD');
 assert.equal(posts,0);
});
