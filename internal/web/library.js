/* The USB browser shares the main page's serialized playback controls. */
(() => {
 const pageSize=20;
 const get=id=>document.getElementById(id);
 let offset=0,request=null,sequence=0,timer=null,resultKey='';
 async function search() {
  if(get('musicBrowser').hidden)return;
  request?.abort();request=new AbortController();
  const activeRequest=request;
  const revision=++sequence;
  const timeout=setTimeout(()=>activeRequest.abort(),15000);
  get('musicPrevious').disabled=true;get('musicNext').disabled=true;
  try {
   const response=await fetch('/api/library?q='+encodeURIComponent(get('musicSearch').value)+'&offset='+offset,{signal:request.signal});
   if(!response.ok)throw Error(await response.text());
   const result=await response.json();
   if(revision!==sequence)return;
   const status=result.status||{},tracks=result.tracks||[];
   if(offset&&offset>=result.total){offset=0;clearTimeout(timeout);return search()}
   const meter=get('libraryScanProgress');
   meter.hidden=!status.scanning;
   const counting=status.phase==='discovering';
   if(counting)meter.removeAttribute('value');else meter.value=status.percent||0;
   let scan='';
   if(status.scanning){
    scan=counting?'Counting MP3 files… '+(status.total||0)+' found · ':status.phase==='finalizing'?'Finishing scan… · ':'Scanning USB music: '+(status.percent||0)+'% · '+(status.processed||0)+' / '+(status.total||0)+' files checked · ';
    scan+=(status.count||0)+' songs saved · ';
   }else if(status.phase==='complete')scan='Scan complete · 100% · ';
   else if(status.phase==='cached')scan='Using saved library · ';
   else if(status.phase==='paused')scan='Scan paused · '+(status.count||0)+' songs saved · Refresh library to continue · ';
   get('libraryStatus').textContent=(status.error?status.error+' · Saved songs are retained. · ':'')+
    scan+result.total+(get('musicSearch').value.trim()?' matching songs':' songs')+
    (result.total?' · '+(offset+1)+'–'+(offset+tracks.length):get('musicSearch').value.trim()?' · No matching songs':status.scanning?'':' · Mount your USB drive and press Refresh library')+
    (status.warnings?' · Some files have unreadable audio headers; available tags and filenames are shown.':'');
   get('musicPage').textContent='Page '+(Math.floor(offset/pageSize)+1)+' of '+Math.max(1,Math.ceil(result.total/pageSize));
   const key=JSON.stringify(tracks);
   if(key!==resultKey){
    resultKey=key;
    get('musicResults').replaceChildren(...tracks.map(track=>{
     const button=document.createElement('button');
     button.type='button';button.style.cssText='display:flex;align-items:center;gap:12px;width:100%;text-align:left;margin:8px 0';
     if(track.cover_url){
      const art=document.createElement('img');art.src=track.cover_url;art.alt='';art.loading='lazy';art.width=48;art.height=48;art.style.objectFit='cover';art.onerror=()=>{art.hidden=true};button.append(art);
     }
     const text=document.createElement('span');text.textContent=track.title+' · '+track.artist+' · '+track.album+' · '+duration(track.duration);button.append(text);
     button.onclick=()=>control('usb-play',undefined,undefined,track.id);
     return button;
    }));
   }
   get('musicPrevious').disabled=offset===0;get('musicNext').disabled=offset+tracks.length>=result.total;
  }catch(error){if(revision===sequence)get('libraryStatus').textContent=error.name==='AbortError'?'Library request timed out; retrying…':error.message}
  finally{clearTimeout(timeout)}
 }
 get('pickSong').onclick=()=>{
  const panel=get('musicBrowser');panel.hidden=!panel.hidden;
  get('pickSong').setAttribute('aria-expanded',String(!panel.hidden));
  if(!panel.hidden){get('musicSearch').focus();search()}else{request?.abort()}
 };
 get('musicSearch').oninput=()=>{
  offset=0;++sequence;request?.abort();clearTimeout(timer);
  timer=setTimeout(search,250);
 };
 get('musicPrevious').onclick=()=>{offset=Math.max(0,offset-pageSize);search()};
 get('musicNext').onclick=()=>{offset+=pageSize;search()};
 get('mixAll').onclick=async()=>{
  get('mixAll').disabled=true;
  try{await control('usb-mix')}finally{get('mixAll').disabled=false}
 };
 get('refreshLibrary').onclick=async()=>{
  get('refreshLibrary').disabled=true;
  try{
   const response=await fetch('/api/library/refresh',{method:'POST',signal:AbortSignal.timeout(15000)});
   if(!response.ok)throw Error(await response.text());
   get('libraryStatus').textContent='Library refresh requested…';
   await search();
  }catch(error){get('libraryStatus').textContent=error.message}
  finally{get('refreshLibrary').disabled=false}
 };
 async function poll(){await search();setTimeout(poll,3000)}
 setTimeout(poll,3000);
})();
