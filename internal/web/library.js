/* USB browsing is paged on the Pi; playback uses the main serialized controller. */
var usbBrowserUpdate = (() => {
 const pageSize=20,get=id=>document.getElementById(id);
 let offset=0,request=null,sequence=0,timer=null,resultKey='',view='home',selected=null;
 let state=null,blocked=false,wasUSB=false,rows=[],total=0,scanning=false,dockArtKey=null,albumStart='';
 let sidebarView='albums',collections={albums:[],artists:[]},collectionTotals={},sidebarKey='';
 const history=[];
 const node=(tag,className,text)=>{const el=document.createElement(tag);el.className=className;if(text!==undefined)el.textContent=text;return el};
 function artwork(item,className='card-art'){
  const art=node('span',className,'♫');art.setAttribute('aria-hidden','true');
  if(item.cover_url){const img=node('img','');img.src=item.cover_url;img.alt='';img.loading='lazy';img.onerror=()=>{art.textContent='♫'};art.replaceChildren(img)}
  return art;
 }
 function update(next,unavailable=false){
  if(next)state=next;
  blocked=unavailable;
  const usb=state?.source==='usb',track=usb?state.usb:null;
  if(usb&&!wasUSB){
   const opening=get('musicBrowser').hidden;
   get('musicBrowser').hidden=false;get('pickSong').setAttribute('aria-expanded','true');
   if(opening&&!get('musicSearch').value.trim())view='home';
   renderView();search();
  }
  wasUSB=usb;
  for(const row of rows){
   const current=usb&&track?.id===row.track.id;
   row.button.setAttribute('aria-current',String(current));
   row.button.setAttribute('aria-label',(current?(state.mpd?.state==='play'?'Pause ':'Resume '):'Play ')+row.track.title+' by '+row.track.artist+' · '+row.track.album);
   row.number.textContent=current?(state.mpd?.state==='play'?'Ⅱ':'▶'):String(row.index);
   row.button.disabled=blocked;
  }
  get('mixAll').disabled=blocked||!total;
  get('mixAll').setAttribute('aria-pressed',String(usb&&!!state.usb_mix));
  get('playCollection').disabled=blocked||!albumStart;
  get('openPlayingAlbum').disabled=!track;
  get('playingTitle').textContent=track?.title||'Choose a song';
  get('dockTitle').textContent=track?.title||'Your USB music';
  get('dockArtist').textContent=track?.artist||'Choose something from your library';
  get('playingAlbumName').textContent=track?.album||'USB Music';
  get('playingQueue').textContent=state?.usb_mix?'Playing a mix of '+state.usb_queue_length+' songs':(state?.usb_queue_length||0)+' songs in this album';
  const artKey=track?.cover_url||'';
  if(dockArtKey!==artKey){dockArtKey=artKey;get('dockCover').replaceChildren(artwork(track||{},'dock-art'))}
 }
 function makeRow(track,index){
  const button=node('button','song-row');button.type='button';
  const number=node('span','song-number');number.setAttribute('aria-hidden','true');
  const main=node('span','song-main'),copy=node('span','song-copy');
  copy.append(node('span','song-title',track.title));copy.append(node('span','song-artist',track.artist));
  main.append(artwork(track,'song-art'));main.append(copy);
  for(const el of [number,main,node('span','song-album',track.album),node('span','song-duration',duration(track.duration))])button.append(el);
  button.onclick=()=>{if(blocked)return;if(state?.source==='usb'&&state.usb?.id===track.id)control('toggle');else control('usb-play',undefined,undefined,track.id)};
  return {button,track,number,index};
 }
 function card(item,kind,style='music-card'){
  const button=node('button',style+(kind==='artists'?' artist-card':''));button.type='button';
  button.setAttribute('aria-label','Open '+(kind==='artists'?'artist ':'album ')+item.title);
  button.append(artwork(item));
  const copy=node('span','card-copy');copy.append(node('span','card-title',item.title));
  copy.append(node('span','card-subtitle',kind==='artists'?'Artist · '+item.count+' songs':item.artist));button.append(copy);
  button.onclick=()=>navigate(kind==='artists'?'artist':'album',item);
  return button;
 }
 function renderSidebar(){
  const items=collections[sidebarView],key=JSON.stringify([sidebarView,items,collectionTotals[sidebarView]]);
  if(key===sidebarKey)return;sidebarKey=key;
  get('sidebarItems').replaceChildren(...items.map(item=>card(item,sidebarView,'sidebar-item')));
  get('sidebarCount').textContent=(collectionTotals[sidebarView]||0)+' '+sidebarView;
  get('sidebarEmpty').hidden=!!items.length;
  get('sideAlbums').setAttribute('aria-pressed',String(sidebarView==='albums'));
  get('sideArtists').setAttribute('aria-pressed',String(sidebarView==='artists'));
 }
 function renderView(){
  const home=view==='home',grid=view==='albums'||view==='artists',detail=view==='album'||view==='artist';
  get('libraryHome').hidden=!home;get('collectionGrid').hidden=!grid;
  get('collectionHeader').hidden=home||view==='songs';get('collectionCover').hidden=!detail;
  get('songsView').hidden=view!=='songs';get('songList').hidden=home||grid;
  get('libraryPagination').hidden=home;get('browseBack').hidden=!history.length;
  get('playCollection').hidden=view!=='album';
  for(const [id,name] of [['viewHome','home'],['viewAlbums','albums'],['viewArtists','artists'],['viewSongs','songs']])get(id).setAttribute('aria-pressed',String(view===name));
  get('collectionKind').textContent=detail?view.toUpperCase():'YOUR LIBRARY';
  get('collectionTitle').textContent=selected?.title||(view==='artists'?'Artists':'Albums');
  get('collectionSubtitle').textContent=selected?(view==='album'?selected.artist:''):'';
  if(detail)get('collectionCover').replaceChildren(artwork(selected));
  get('songsHeading').textContent=get('musicSearch').value.trim()?'Search results':'All songs';
 }
 function navigate(next,item=null,remember=true){
  if(remember)history.push({view,selected,offset,albumStart,query:get('musicSearch').value});
  view=next;selected=item;offset=0;resultKey='';albumStart='';
  get('musicSearch').value='';clearTimeout(timer);++sequence;request?.abort();
  get('libraryEmpty').hidden=true;get('musicResults').replaceChildren();rows=[];
  get('collectionGrid').replaceChildren();renderView();get('librarySection').scrollTop=0;get('libraryStatus').textContent='Loading your music…';search();
 }
 function apiURL(kind,query,page=offset){
  const base='/api/library?q='+encodeURIComponent(query)+'&offset='+page;
  return base+(kind==='songs'?'':'&view='+kind)+(selected&&['album','artist'].includes(kind)?'&id='+encodeURIComponent(selected.id):'');
 }
 async function search(){
  if(get('musicBrowser').hidden)return;
  request?.abort();request=new AbortController();const activeRequest=request,revision=++sequence;
  const timeout=setTimeout(()=>activeRequest.abort(),15000),query=get('musicSearch').value;
  get('musicPrevious').disabled=true;get('musicNext').disabled=true;
  try{
   const fetchResult=async kind=>{const r=await fetch(apiURL(kind,query),{signal:activeRequest.signal});if(!r.ok)throw Error(await r.text());return r.json()};
   const results=await Promise.all(view==='home'?[fetchResult('albums'),fetchResult('artists')]:[fetchResult(view)]);
   if(revision!==sequence)return;
   const result=results[0],status=result.status||{},tracks=result.tracks||[],items=result.collections||[];
   total=status.count??result.total??0;scanning=!!status.scanning;
   if(view==='album'&&offset===0)albumStart=tracks[0]?.id||'';
   if(selected)get('collectionSubtitle').textContent=(view==='album'?selected.artist+' · ':'')+result.total+' songs';
   if(offset&&offset>=result.total){offset=0;clearTimeout(timeout);return search()}
   const meter=get('libraryScanProgress');meter.hidden=!scanning;
   const counting=status.phase==='discovering';if(counting)meter.removeAttribute('value');else meter.value=status.percent||0;
   let scan='';
   if(scanning){scan=counting?'Counting MP3 files… '+(status.total||0)+' found · ':status.phase==='finalizing'?'Finishing scan… · ':'Scanning USB music: '+(status.percent||0)+'% · '+(status.processed||0)+' / '+(status.total||0)+' files checked · ';scan+=(status.count||0)+' songs saved · '}
   else if(status.phase==='complete')scan='Scan complete · 100% · ';
   else if(status.phase==='cached')scan='Using saved library · ';
   else if(status.phase==='paused')scan='Scan paused · '+(status.count||0)+' songs saved · Refresh library to continue · ';
   const noun=view==='home'||view==='albums'?'albums':view==='artists'?'artists':'songs';
   get('libraryStatus').textContent=(status.error?status.error+' · Saved songs are retained. · ':'')+scan+result.total+(query.trim()?' matching ':' ')+noun+
    (result.total&&view!=='home'?' · '+(offset+1)+'–'+(offset+(items.length||tracks.length)):result.total?'':query.trim()?' · No matching '+noun:scanning?'':' · Mount your USB drive and press Refresh library')+
    (status.warnings?' · Some files have unreadable audio headers; available tags and filenames are shown.':'');
   get('musicPage').textContent='Page '+(Math.floor(offset/pageSize)+1)+' of '+Math.max(1,Math.ceil(result.total/pageSize));
   get('libraryEmpty').hidden=!!result.total;
   get('libraryEmpty').textContent=query.trim()?'Nothing found. Try another artist, album, or song.':scanning?'Finding your music… Your collection will appear as it is scanned.':'Your library is empty. Connect your USB drive and refresh the library.';
   const key=JSON.stringify([view,offset,results.map(r=>[r.tracks,r.collections])]);
   if(key!==resultKey){
    resultKey=key;
    if(view==='home'){
     collections.albums=items;collections.artists=results[1].collections||[];
     collectionTotals.albums=result.total;collectionTotals.artists=results[1].total;
     get('quickPicks').replaceChildren(...items.slice(0,8).map(item=>card(item,'albums','quick-pick')));
     get('homeAlbums').replaceChildren(...items.slice(0,8).map(item=>card(item,'albums')));
     get('homeArtists').replaceChildren(...collections.artists.slice(0,8).map(item=>card(item,'artists')));
    }else if(view==='albums'||view==='artists'){
     collections[view]=items;collectionTotals[view]=result.total;sidebarView=view;
     get('collectionGrid').replaceChildren(...items.map(item=>card(item,view)));
    }else{rows=tracks.map((track,i)=>makeRow(track,offset+i+1));get('musicResults').replaceChildren(...rows.map(row=>row.button))}
    renderSidebar();
   }
   update(null,blocked);
   get('musicPrevious').disabled=offset===0;get('musicNext').disabled=offset+(items.length||tracks.length)>=result.total;
  }catch(error){if(revision===sequence)get('libraryStatus').textContent=error.name==='AbortError'?'Library request timed out; retrying…':error.message}
  finally{clearTimeout(timeout)}
 }
 get('pickSong').onclick=()=>{
  const panel=get('musicBrowser');panel.hidden=!panel.hidden;get('pickSong').setAttribute('aria-expanded',String(!panel.hidden));
  if(!panel.hidden){view='songs';renderView();get('musicSearch').focus();search()}else{++sequence;request?.abort()}
 };
 get('musicSearch').oninput=()=>{
  offset=0;selected=null;view=get('musicSearch').value.trim()?'songs':'home';resultKey='';
  ++sequence;request?.abort();clearTimeout(timer);renderView();timer=setTimeout(search,250);
 };
 for(const [id,next] of [['viewHome','home'],['viewAlbums','albums'],['viewArtists','artists'],['viewSongs','songs'],['sideAlbums','albums'],['sideArtists','artists'],['sideSongs','songs'],['showAlbums','albums'],['showArtists','artists']])get(id).onclick=()=>navigate(next);
 get('usbHome').onclick=()=>{get('playerTab').onclick?.();get('musicBrowser').hidden=false;navigate('home')};
 get('sideShowAll').onclick=()=>navigate(sidebarView);
 get('browseBack').onclick=()=>{const previous=history.pop();if(!previous)return;({view,selected,offset,albumStart}=previous);get('musicSearch').value=previous.query;resultKey='';clearTimeout(timer);renderView();search()};
 get('openPlayingAlbum').onclick=()=>{const track=state?.usb;if(track)navigate('album',{id:track.id,title:track.album,artist:track.artist,cover_url:track.cover_url,count:state.usb_mix?'':state.usb_queue_length})};
 get('playCollection').onclick=()=>{if(!blocked&&albumStart)control('usb-play',undefined,undefined,albumStart)};
 get('musicPrevious').onclick=()=>{offset=Math.max(0,offset-pageSize);search()};get('musicNext').onclick=()=>{offset+=pageSize;search()};
 get('mixAll').onclick=async()=>{if(blocked||!total)return;get('mixAll').disabled=true;try{await control('usb-mix')}finally{get('mixAll').disabled=blocked||!total}};
 get('refreshLibrary').onclick=async()=>{
  get('refreshLibrary').disabled=true;
  try{const r=await fetch('/api/library/refresh',{method:'POST',signal:AbortSignal.timeout(15000)});if(!r.ok)throw Error(await r.text());get('libraryStatus').textContent='Library refresh requested…';await search()}
  catch(error){get('libraryStatus').textContent=error.message}finally{get('refreshLibrary').disabled=false}
 };
 async function poll(){await search();setTimeout(poll,scanning||['songs','album','artist'].includes(view)?3000:15000)}
 renderView();setTimeout(poll,3000);return update;
})();
