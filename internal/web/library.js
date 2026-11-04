/* The USB browser shares the main page's serialized playback controls. */
var usbBrowserUpdate = (() => {
 const pageSize=20;
 const get=id=>document.getElementById(id);
 let offset=0,request=null,sequence=0,timer=null,resultKey='';
 let state=null,blocked=false,wasUSB=false,rows=[],total=0;
 function update(next,unavailable=false){
  if(next)state=next;
  blocked=unavailable;
  const usb=state?.source==='usb';
  if(usb&&!wasUSB){
   get('musicBrowser').hidden=false;
   get('pickSong').setAttribute('aria-expanded','true');
   search();
  }
  wasUSB=usb;
  for(const {button,track,number,index} of rows){
   const current=usb&&state.usb?.id===track.id;
   button.setAttribute('aria-current',String(current));
   button.setAttribute('aria-label',(current?(state.mpd?.state==='play'?'Pause ':'Resume '):'Play ')+track.title+' by '+track.artist+' · '+track.album);
   number.textContent=current?(state.mpd?.state==='play'?'Ⅱ':'▶'):String(index);
   button.disabled=blocked;
  }
  get('mixAll').disabled=blocked||!total;
  get('mixAll').setAttribute('aria-pressed',String(usb&&!!state.usb_mix));
 }
 function makeRow(track,index){
  const button=document.createElement('button');button.type='button';button.className='song-row';
  const number=document.createElement('span');number.className='song-number';number.setAttribute('aria-hidden','true');
  const main=document.createElement('span');main.className='song-main';
  const art=document.createElement('span');art.className='song-art';art.textContent='♫';art.setAttribute('aria-hidden','true');
  if(track.cover_url){
   const img=document.createElement('img');img.src=track.cover_url;img.alt='';img.loading='lazy';img.width=44;img.height=44;
   img.onerror=()=>{art.textContent='♫'};art.replaceChildren(img);
  }
  const copy=document.createElement('span');copy.className='song-copy';
  const title=document.createElement('span');title.className='song-title';title.textContent=track.title;
  const artist=document.createElement('span');artist.className='song-artist';artist.textContent=track.artist;
  copy.append(title);copy.append(artist);main.append(art);main.append(copy);
  const album=document.createElement('span');album.className='song-album';album.textContent=track.album;
  const time=document.createElement('span');time.className='song-duration';time.textContent=duration(track.duration);
  for(const node of [number,main,album,time])button.append(node);
  button.onclick=()=>{
   if(blocked)return;
   if(state?.source==='usb'&&state.usb?.id===track.id)control('toggle');
   else control('usb-play',undefined,undefined,track.id);
  };
  return {button,track,number,index};
 }
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
   total=status.count??result.total??0;
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
   get('libraryEmpty').hidden=!!tracks.length;
   get('libraryEmpty').textContent=get('musicSearch').value.trim()?'No songs found. Try another artist, album, or title.':status.scanning?'Finding your music… Songs will appear as they are scanned.':'Your library is empty. Connect your USB drive and refresh the library.';
   const key=JSON.stringify([offset,tracks]);
   if(key!==resultKey){
    resultKey=key;
    rows=tracks.map((track,i)=>makeRow(track,offset+i+1));
    get('musicResults').replaceChildren(...rows.map(row=>row.button));
   }
   update(null,blocked);
   get('musicPrevious').disabled=offset===0;get('musicNext').disabled=offset+tracks.length>=result.total;
  }catch(error){if(revision===sequence)get('libraryStatus').textContent=error.name==='AbortError'?'Library request timed out; retrying…':error.message}
  finally{clearTimeout(timeout)}
 }
 get('pickSong').onclick=()=>{
  const panel=get('musicBrowser');panel.hidden=!panel.hidden;
  get('pickSong').setAttribute('aria-expanded',String(!panel.hidden));
  if(!panel.hidden){get('musicSearch').focus();search()}else{++sequence;request?.abort()}
 };
 get('musicSearch').oninput=()=>{
  offset=0;++sequence;request?.abort();clearTimeout(timer);
  timer=setTimeout(search,250);
 };
 get('musicPrevious').onclick=()=>{offset=Math.max(0,offset-pageSize);search()};
 get('musicNext').onclick=()=>{offset+=pageSize;search()};
 get('mixAll').onclick=async()=>{
  if(blocked||!total)return;
  get('mixAll').disabled=true;
  try{await control('usb-mix')}finally{get('mixAll').disabled=blocked||!total}
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
 return update;
})();
