const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');

test('display dims after two minutes and wakes on touch, keyboard and player controls',()=>{
 const html=fs.readFileSync(__dirname+'/display.html','utf8');
 const script=html.slice(html.indexOf('const dimTimeout='),html.indexOf('function render(s)'));
 let now=1000,dimmed;
 const listeners={};
 const context=vm.createContext({Date:{now:()=>now},$ :()=>({setAttribute:(_,value)=>{dimmed=value}}),document:{addEventListener:(event,handler)=>{listeners[event]=handler}}});
 vm.runInContext(script,context);
 const update=()=>vm.runInContext('updateDimming()',context);
 now+=119999;update();assert.equal(dimmed,'false');
 now++;update();assert.equal(dimmed,'true');
 for(const event of ['pointerdown','pointermove','keydown']){
  listeners[event]();assert.equal(dimmed,'false');
  now+=120000;update();assert.equal(dimmed,'true');
 }
 // Polling the same activity must not reset the timer; a new command must.
 assert.match(html,/s.screen_activity!==screenActivity/);
 vm.runInContext('brighten()',context);assert.equal(dimmed,'false');
});
