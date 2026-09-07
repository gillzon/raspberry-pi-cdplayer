const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
test('settings loads named outputs, applies selection and reports failures',async()=>{
 const elements=new Map();
 const element=()=>({dataset:{},hidden:false,disabled:false,textContent:'',value:'',setAttribute(){},removeAttribute(){},replaceChildren(...children){this.children=children}});
 const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 let reject=false;const posts=[];
 const context=vm.createContext({
 document:{getElementById:get,querySelector:element,querySelectorAll:()=>[],createElement:element},
 TrackNavigation:class{update(){}cancel(){}},AbortSignal,setTimeout:()=>{},Date,
 fetch:async(url,options)=>{
  if(options?.method==='POST'){const body=JSON.parse(options.body);posts.push(body);return{ok:!reject,text:async()=> 'Output unavailable'}}
  return{ok:true,json:async()=>url==='/api/system'?{updated:new Date().toISOString()}:{disc:{Tracks:[]},outputs:[{name:'stable HDMI name',label:'HDMI 1',enabled:true,device:'plughw:CARD=vc4hdmi0,DEV=0'}]}}
 }
 });
 const html=fs.readFileSync(__dirname+'/index.html','utf8');
 vm.runInContext([...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n'),context);
 await vm.runInContext('loadOutputs()',context);
 assert.equal(get('soundOutput').children[0].textContent,'HDMI 1 · CD + Spotify');
 assert.equal(get('applyOutput').disabled,false);
 get('soundOutput').value='stable HDMI name';
 await get('applyOutput').onclick();
 assert.deepEqual(posts.at(-1),{action:'output',output:'stable HDMI name'});
 assert.match(get('outputStatus').textContent,/saved/);
 reject=true;await get('applyOutput').onclick();
 assert.match(get('outputStatus').textContent,/unavailable/);
 assert.equal(get('applyOutput').disabled,false);
});
