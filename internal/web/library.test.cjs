const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const tick=()=>new Promise(resolve=>setImmediate(resolve));

function element(){return {style:{},dataset:{},hidden:false,disabled:false,textContent:'',value:'',children:[],setAttribute(){},removeAttribute(){},focus(){},append(e){this.children.push(e)},replaceChildren(...e){this.children=e}}}

test('USB browser searches one field, renders safe text, pages and selects by ID',async()=>{
 const elements=new Map();const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 get('musicBrowser').hidden=true;
 const calls=[],plays=[];
 let result={tracks:[{id:'abc',title:'<script>bad()</script>',artist:'Björk',album:'Debut',cover_url:'/api/library/art/abc',duration:60}],total:21,status:{}};
 const context=vm.createContext({document:{getElementById:get,createElement:element},duration:()=> '1:00',control:(...args)=>plays.push(args),
  fetch:async(url,options)=>{calls.push([url,options]);return {ok:true,json:async()=>result}},
  AbortController,AbortSignal,setTimeout:()=>1,clearTimeout(){},encodeURIComponent});
 vm.runInContext(fs.readFileSync(__dirname+'/library.js','utf8'),context);
 get('musicSearch').value='bjork debut';
 get('pickSong').onclick();await tick();
 assert.equal(calls[0][0],'/api/library?q=bjork%20debut&offset=0');
 const button=get('musicResults').children[0];
 assert.match(button.children[1].textContent,/^<script>bad\(\)<\/script> · Björk/);
 button.onclick();assert.deepEqual(plays[0],['usb-play',undefined,undefined,'abc']);
 assert.equal(get('musicNext').disabled,false);
 result={...result,total:21};get('musicNext').onclick();await tick();
 assert.match(calls.at(-1)[0],/offset=20$/);
 assert.equal(get('musicPrevious').disabled,false);
 assert.equal(get('musicNext').disabled,true);
 assert.equal(get('musicPage').textContent,'Page 2 of 2');
 await get('mixAll').onclick();
 assert.deepEqual(plays[1],['usb-mix']);
 assert.equal(get('mixAll').disabled,false);
 await get('refreshLibrary').onclick();
 assert.ok(calls.some(([url,options])=>url==='/api/library/refresh'&&options.method==='POST'));
 result={...result,status:{scanning:true,phase:'discovering',total:1200,count:500}};
 await get('refreshLibrary').onclick();
 assert.match(get('libraryStatus').textContent,/Counting MP3 files… 1200 found/);
 assert.equal(get('libraryScanProgress').hidden,false);
 result={...result,status:{scanning:true,phase:'indexing',percent:42,processed:420,total:1000,count:600}};
 await get('refreshLibrary').onclick();
 assert.equal(get('libraryScanProgress').value,42);
 assert.match(get('libraryStatus').textContent,/42% · 420 \/ 1000 files checked · 600 songs saved/);
 result={...result,status:{scanning:false,phase:'interrupted',error:'Drive disconnected'}};
 await get('refreshLibrary').onclick();
 assert.equal(get('libraryScanProgress').hidden,true);
 assert.match(get('libraryStatus').textContent,/Drive disconnected · Saved songs are retained/);
 assert.equal(get('musicResults').children.length,1);
 result={...result,status:{scanning:false,phase:'complete',percent:100}};
 await get('refreshLibrary').onclick();
 assert.match(get('libraryStatus').textContent,/Scan complete · 100%/);
 result={...result,total:0,tracks:[],status:{phase:'cached',count:27362}};
 await get('refreshLibrary').onclick();
 assert.match(get('libraryStatus').textContent,/Using saved library · 0 matching songs/);
 assert.equal(get('libraryScanProgress').hidden,true);
 result={...result,status:{phase:'paused',count:5794}};
 await get('refreshLibrary').onclick();
 assert.match(get('libraryStatus').textContent,/Scan paused · 5794 songs saved · Refresh library to continue/);
});

test('USB now-playing and next use MPD even with no CD inserted',async()=>{
 const elements=new Map();const get=id=>{if(!elements.has(id))elements.set(id,element());return elements.get(id)};
 const controls=['play','pause','stop','next','previous','eject'].map(action=>Object.assign(element(),{dataset:{action}}));
 const switchCD=get('switchCD');switchCD.dataset.action='source-cd';controls.push(switchCD);
 const posts=[];
 const state={source:'usb',updated:new Date().toISOString(),disc:{ID:'',Tracks:[]},mpd:{state:'play',song:'0'},usb:{title:'A song',artist:'An artist',album:'An album',cover_url:'/api/library/art/abc'},usb_queue_length:2};
 const context=vm.createContext({document:{getElementById:get,querySelector:()=>controls[0],querySelectorAll:s=>s==='[data-action]'||s==='[data-action],[data-track]'?controls:[],createElement:element},
  TrackNavigation:class{update(){}cancel(){}step(){throw Error('CD navigation used for USB')}},
  fetch:async(url,options)=>{if(options?.method==='POST')posts.push(JSON.parse(options.body));return {ok:true,json:async()=>url==='/api/system'?{}:state}},AbortSignal,setTimeout(){},Date});
 const html=fs.readFileSync(__dirname+'/index.html','utf8');
 vm.runInContext([...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(m=>m[1]).join('\n'),context);
 await vm.runInContext('refresh()',context);
 assert.equal(get('now').textContent,'Playing · A song');
 assert.equal(get('albumTitle').textContent,'An album');
 assert.equal(get('coverArt').src,'/api/library/art/abc');
 assert.equal(switchCD.hidden,false);
 assert.equal(get('tracks').hidden,true);
 assert.equal(controls[0].disabled,false);
 controls.find(b=>b.dataset.action==='next').onclick();await tick();
 assert.equal(posts[0].action,'next');
});
