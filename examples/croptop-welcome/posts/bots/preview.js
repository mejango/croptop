export default function(el){
 function drawCroptopArt(ctx, options) {
 const {kind,width,height,phase=0,density=12,peers=4,online=true,layout='grid',yaw=0,pitch=0,face=null}=options;
 const ink='#171712',paper='#f3e4c4';
 ctx.save();const scale=Math.min(width,height)/200;ctx.translate((width-200*scale)/2,(height-200*scale)/2);ctx.scale(scale,scale);ctx.lineCap='round';ctx.lineJoin='round';
 let mark=0;
 const noise=(i)=>Math.sin(i*19.19+3)*.65+Math.sin(i*7.17)*.45;
 const shape=(points,fill=paper,closed=true,weight=4.5)=>{
  const seed=mark++*11;ctx.beginPath();points.forEach(([x,y],i)=>{x+=noise(i+seed);y+=noise(i+seed+17);if(i)ctx.lineTo(x,y);else ctx.moveTo(x,y)});if(closed)ctx.closePath();ctx.fillStyle=fill;if(fill)ctx.fill();ctx.strokeStyle=ink;ctx.lineWidth=weight;ctx.stroke();
 };
 const line=(x,y,x2,y2,weight=4)=>{
  const n=12,points=[];for(let i=0;i<=n;i++){const t=i/n;points.push([x+(x2-x)*t+Math.sin(t*Math.PI)*noise(i+mark),y+(y2-y)*t+Math.sin(t*Math.PI)*noise(i+mark+8)])}shape(points,null,false,weight);
 };
 const oval=(cx,cy,rx,ry,fill=paper,weight=4.5,rotation=0)=>{
  const points=[];for(let i=0;i<42;i++){const a=i/42*Math.PI*2;const x=Math.cos(a)*rx,y=Math.sin(a)*ry;points.push([cx+x*Math.cos(rotation)-y*Math.sin(rotation),cy+x*Math.sin(rotation)+y*Math.cos(rotation)])}shape(points,fill,true,weight);
 };
 const page=(x,y,w,h)=>shape([[x+2,y],[x+w-2,y+1],[x+w,y+h-2],[x,y+h],[x+2,y]]);
 if(kind==='site'){
  page(36,28,130,146);line(38,53,164,54,3);[48,59,70].forEach(x=>oval(x,41,1.3,1.3,ink,1));
  line(51,70,112,70,4);
  if(layout==='grid')for(let i=0;i<3;i++){const x=49+i*35;page(x,91,26,61);line(x+5,139,x+19,138,2.2);if(i===1)oval(x+13,113,6,9,ink,2);}
  else for(let i=0;i<3;i++){const y=91+i*24;page(50,y,15,15);line(76,y+4,146-i*10,y+4,3);line(76,y+11,123,y+11,2)}
 }else if(kind==='start'){
  // A small entrance: open the door and make a place of your own.
  shape([[48,153],[48,40],[137,40],[137,153]],paper,true,4.5);
  shape([[58,151],[58,50],[126,50],[126,151]],ink,true,3);
  shape([[60,51],[111,33],[111,145],[60,163]],'#d9b77c',true,4);
  oval(99,98,2.5,3,paper,2);
  shape([[68,168],[113,166],[126,176],[59,179]],paper,true,3);
  shape([[82,186],[125,182],[140,191],[91,197]],paper,true,3);
  line(34,62,24,59,2.5);line(38,44,32,36,2.5);line(150,73,160,68,2.5);
 }else if(kind==='writing'){
  page(46,28,113,145);line(61,61,132,60,3.5);line(61,80,137,81,3);line(61,101,126,101,3);line(61,122,138,121,3);line(61,143,106,144,3);
 }else if(kind==='audio'){
  ctx.translate(100,100);ctx.rotate(-.32);ctx.translate(-100,-100);
  oval(90,123,39,48);shape([[91,100],[98,31],[111,29],[109,104]],paper,true,4);shape([[98,31],[99,17],[113,18],[111,31]],paper,true,4);oval(91,119,12,13,ink,2);
  for(let i=0;i<3;i++)line(86+i*5,151,101+i*3,25,1.4);line(76,151,105,151,5);for(let i=0;i<3;i++)line(99,21+i*5,94,20+i*5,3);
 }else if(kind==='video'){
  page(24,47,152,105);line(33,164,168,163,5);shape([[87,76],[122,101],[87,124]],ink,true,3);line(52,37,72,28,3);line(130,30,151,38,3);
 }else if(kind==='studio'){
  shape([[72,130],[60,36],[78,65],[108,115],[128,134],[116,146],[99,124]],paper,true,5);
  shape([[126,130],[141,33],[122,61],[96,113],[70,134],[80,148],[100,126]],paper,true,5);
  oval(66,148,24,21,paper,5,-.4);oval(134,149,24,21,paper,5,.4);oval(99,116,4,4,ink,2);
 }else if(kind==='art'){
  const petals=Math.min(18,Math.max(8,density)),cx=100+Math.sin(phase*.5)*4,cy=81;
  line(cx,104,94,182,5);shape([[96,151],[67,124],[51,131],[61,153],[96,165]],paper,true,4);shape([[99,135],[121,113],[141,120],[131,142],[98,150]],paper,true,4);
  for(let i=0;i<petals;i++){const a=i/petals*Math.PI*2+Math.sin(phase*.3)*.06;oval(cx+Math.cos(a)*32,cy+Math.sin(a)*32,20,8,paper,3.6,a)}oval(cx,cy,20,21,paper,4.5);
  if(face===null){oval(cx-6,cy-3,2,2,ink,1);oval(cx+7,cy-3,2,2,ink,1);line(cx-4,cy+7,cx+5,cy+8,2);}
  else {
   // The seven expressions from the flower animation, on the original single bloom.
   const dot=(x,y)=>oval(cx+x,cy+y,1.8,1.8,ink,1);
   const stroke=(x,y,x2,y2)=>line(cx+x,cy+y,cx+x2,cy+y2,2.3);
   const smile=(down=false)=>{ctx.beginPath();ctx.moveTo(cx-6,cy+8);ctx.quadraticCurveTo(cx,cy+(down?0:16),cx+6,cy+8);ctx.strokeStyle=ink;ctx.lineWidth=2.5;ctx.stroke();};
   if(face===0){dot(-6,-3);dot(6,-3);smile();}
   else if(face===1){stroke(-8,-3,-4,-3);stroke(4,-3,8,-3);smile();}
   else if(face===2){dot(-6,-3);stroke(4,-3,8,-3);smile();}
   else if(face===3){dot(-6,-3);dot(6,-3);oval(cx,cy+9,3.6,4.4,null,2.3);}
   else if(face===4){dot(-6,-3);dot(6,-3);stroke(-10,-11,-4,-7);stroke(4,-7,10,-11);smile(true);}
   else if(face===5){for(const x of [-6,6]){stroke(x-2,-5,x+2,-1);stroke(x+2,-5,x-2,-1);}stroke(-4,8,4,10);}
   else {dot(-6,-3);dot(6,-3);stroke(-6,8,6,8);}
  }
 }else if(kind==='network'){
  // A growing peer mesh: a ring unfolds into a sphere, without a central node.
  const count=peers+1,spread=Math.min(1,Math.max(0,(count-9)/8));
  const turn=yaw+phase*.09,tilt=pitch-.12,cyaw=Math.cos(turn),syaw=Math.sin(turn),cp=Math.cos(tilt),sp=Math.sin(tilt);
  const project=({x,y,z})=>{const rx=x*cyaw+z*syaw,rz=-x*syaw+z*cyaw,ry=y*cp-rz*sp,depth=y*sp+rz*cp,f=260/(260-depth);return {x:100+rx*f,y:96+ry*f,z:depth,f}};
  const nodes=Array.from({length:count},(_,id)=>{const a=id/count*Math.PI*2-Math.PI/2,sy=1-2*(id+.5)/count,sr=Math.sqrt(1-sy*sy),sa=id*Math.PI*(3-Math.sqrt(5));const world={x:(Math.cos(a)*(1-spread)+Math.cos(sa)*sr*spread)*68,y:(Math.sin(a)*(1-spread)+sy*spread)*68,z:Math.sin(sa)*sr*spread*68};return {id,world,...project(world)}});
  const live=online?nodes:nodes.slice(1),edges=new Map();
  const connect=(a,b)=>{if(a.id===b.id)return;const key=Math.min(a.id,b.id)+':'+Math.max(a.id,b.id);edges.set(key,[a,b])};
  live.forEach((node,i)=>{if(live.length<2)return;connect(node,live[(i+1)%live.length]);const nearby=live.filter(n=>n!==node).sort((a,b)=>{const d=n=>(n.world.x-node.world.x)**2+(n.world.y-node.world.y)**2+(n.world.z-node.world.z)**2;return d(a)-d(b)});nearby.slice(0,2).forEach(n=>connect(node,n));});
  [...edges.values()].sort((a,b)=>(a[0].z+a[1].z)-(b[0].z+b[1].z)).forEach(([a,b])=>{
   ctx.save();ctx.globalAlpha=.22+((a.z+b.z)/2+80)/160*.4;line(a.x,a.y,b.x,b.y,count>20?.7:1.3);
   const t=(phase*.16+(a.id+b.id)*.17)%1;const packet=project({x:a.world.x+(b.world.x-a.world.x)*t,y:a.world.y+(b.world.y-a.world.y)*t,z:a.world.z+(b.world.z-a.world.z)*t});oval(packet.x,packet.y,count>20?.7:1.4,count>20?.7:1.4,ink,.5);ctx.restore();
  });
  const size=Math.max(.3,1-Math.max(0,count-8)*.017);
  nodes.sort((a,b)=>a.z-b.z).forEach(({id,x,y,z,f})=>{
   ctx.save();ctx.translate(x,y);ctx.scale(size*f,size*f);ctx.globalAlpha=(.48+(z+80)/160*.52)*(id===0&&!online?.38:1);
   page(-12,-16,24,32);
   if(id===0&&!online){line(-6,-8,6,8,2);line(6,-8,-6,8,2)}else{line(-6,-6,6,-6,2);line(-6,3,4,3,2)}
   if(id===0){ctx.fillStyle=ink;ctx.font='8px sans-serif';ctx.textAlign='center';ctx.fillText('you',0,27)}ctx.restore();
  });
 }else if(kind==='sun'){
  for(let i=0;i<14;i++){const a=i/14*Math.PI*2+phase*.06;line(100+Math.cos(a)*57,100+Math.sin(a)*57,100+Math.cos(a)*72,100+Math.sin(a)*72,4.5)}
  oval(100,100,47,49);oval(86,94,3,4,ink,2);oval(115,94,3,4,ink,2);shape([[100,97],[96,110],[102,111]],null,false,2.8);shape([[90,119],[98,122],[109,118]],null,false,3);
 }else{
  shape([[100,48],[65,36],[30,43],[31,150],[67,144],[99,162],[137,146],[172,151],[170,43],[138,36]],paper,true,5);line(100,50,99,160,4);
  for(let i=0;i<4;i++){line(43,65+i*19,82,68+i*19,3);line(116,69+i*19,157,65+i*19,3)}
 }
 ctx.restore();
}

// A seeded garden: stable plants sway and open at their own pace.
function createCroptopGarden(){
 const seed=new Uint32Array(1);crypto.getRandomValues(seed);let state=seed[0];
 const random=()=>{state=(Math.imul(state,1664525)+1013904223)>>>0;return state/4294967296;};
 return Array.from({length:11},(_,i)=>({x:.08+(i%6)/5*.84+(random()-.5)*.07,y:i<6?.65+random()*.1:.9+random()*.04,size:.23+random()*.16,face:i%7,petals:5+Math.floor(random()*10),offset:random()*20,color:['#f3e4c4','#dda098','#bbc398','#e7bd6c'][Math.floor(random()*4)],lean:(random()-.5)*.2}));
}
function drawCroptopGarden(ctx,{width,height,phase=0,garden,density=11,petalCount}){
 ctx.save();ctx.lineJoin='round';ctx.lineCap='round';
 for(const plant of garden.slice(0,density)){
  const size=Math.min(width,height*1.5)*plant.size,x=plant.x*width,y=plant.y*height;
  const cycle=0;
  const variation=n=>{const v=Math.sin(plant.offset*31+cycle*17+n*13)*43758.5453;return v-Math.floor(v);};
  const petals=petalCount||5+Math.floor(variation(1)*10),face=plant.face,color=['#f3e4c4','#dda098','#bbc398','#e7bd6c'][Math.floor(variation(3)*4)];
  const sway=Math.sin(phase*.5+plant.offset)*.035+plant.lean;
  ctx.save();ctx.translate(x,y);ctx.rotate(sway);ctx.scale(size/100,size/100);ctx.strokeStyle='#171712';ctx.lineWidth=2.2;
  ctx.beginPath();ctx.moveTo(0,0);ctx.bezierCurveTo(-2,-20,5,-40,0,-59);ctx.stroke();
  for(const side of [-1,1]){ctx.beginPath();ctx.moveTo(0,-18);ctx.quadraticCurveTo(side*25,-20,side*24,-37);ctx.quadraticCurveTo(side*4,-38,0,-18);ctx.fillStyle='#bbc398';ctx.fill();ctx.stroke();}
  for(let i=0;i<petals;i++){const angle=i/petals*Math.PI*2+Math.sin(phase*.3)*.06;ctx.save();ctx.translate(0,-64);ctx.rotate(angle);ctx.beginPath();for(let point=0;point<32;point++){const a=point/32*Math.PI*2,n=Math.sin(point*7.17+i)*.35;const px=18+Math.cos(a)*13+n,py=Math.sin(a)*5.3+n*.5;if(point)ctx.lineTo(px,py);else ctx.moveTo(px,py);}ctx.closePath();ctx.fillStyle=color;ctx.fill();ctx.stroke();ctx.restore();}
  ctx.beginPath();ctx.ellipse(0,-64,9,9.5,0,0,Math.PI*2);ctx.fillStyle='#f3e4c4';ctx.fill();ctx.stroke();
  ctx.fillStyle='#171712';ctx.lineWidth=1.3;
  const dot=(x,y,r=1)=>{ctx.beginPath();ctx.arc(x,y,r,0,Math.PI*2);ctx.fill();};
  const line=(x,y,x2,y2)=>{ctx.beginPath();ctx.moveTo(x,y);ctx.lineTo(x2,y2);ctx.stroke();};
  const smile=(down=false)=>{ctx.beginPath();ctx.moveTo(-3,-60);ctx.quadraticCurveTo(0,down?-64:-56,3,-60);ctx.stroke();};
  if(face===0){dot(-3,-65);dot(3,-65);smile();}
  else if(face===1){line(-4,-65,-2,-65);line(2,-65,4,-65);smile();}
  else if(face===2){dot(-3,-65);line(2,-65,4,-65);smile();}
  else if(face===3){dot(-3,-65);dot(3,-65);ctx.beginPath();ctx.ellipse(0,-59,1.8,2.2,0,0,Math.PI*2);ctx.stroke();}
  else if(face===4){dot(-3,-65);dot(3,-65);line(-5,-69,-2,-67);line(2,-67,5,-69);smile(true);}
  else if(face===5){for(const x of [-3,3]){line(x-1,-66,x+1,-64);line(x+1,-66,x-1,-64);}line(-2,-60,2,-59);}
  else {dot(-3,-65);dot(3,-65);line(-3,-60,3,-60);}
  ctx.restore();
 }
 // A wandering pollinator quietly changes its path through the flowers.
 const bx=width*(.5+.36*Math.sin(phase*.45)),by=height*(.23+.12*Math.sin(phase*.65+2));ctx.translate(bx,by);ctx.strokeStyle='#171712';ctx.lineWidth=1.5;const wing=3+2*Math.sin(phase*9);ctx.beginPath();ctx.ellipse(-3,-2,3,wing,-.5,0,Math.PI*2);ctx.ellipse(3,-2,3,wing,.5,0,Math.PI*2);ctx.stroke();ctx.restore();
}

 function mountCroptopRhythm(host, compact=false) {
 if(host.dataset.rhythmReady)return;host.dataset.rhythmReady='true';
 if(!document.getElementById('ct-rhythm-style')){
  const style=document.createElement('style');style.id='ct-rhythm-style';style.textContent=`
  .ct-rhythm{color:#171712;font-family:var(--font-family,system-ui);width:100%;box-sizing:border-box}.ct-rhythm *{box-sizing:border-box}.ct-rhythm-grid{display:grid;grid-template-columns:86px repeat(8,minmax(0,1fr));gap:8px;align-items:center}.ct-rhythm-track{font:inherit;font-size:12px;width:100%;min-width:0;padding:8px 2px;border:1px solid #17171255;border-radius:2px;background:#f3e4c4;color:#171712;cursor:pointer}.ct-rhythm button.ct-rhythm-step{appearance:none;min-width:0!important;min-height:0!important;width:100%!important;aspect-ratio:1;max-height:66px;padding:0!important;border:2px solid #171712!important;border-radius:4px 2px 5px 2px!important;background:#f3e4c4!important;color:#171712!important;cursor:pointer;display:grid;place-items:center}.ct-rhythm button.ct-rhythm-step[aria-pressed=true]{background:#171712!important;color:#f3e4c4!important}.ct-rhythm-step::after{content:'';width:28%;height:28%;border:2px solid currentColor;border-radius:50%}.ct-rhythm-step[data-track='1']::after{width:26%;height:26%;border-radius:0;transform:rotate(45deg)}.ct-rhythm-step[data-track='2']::after{width:26%;height:34%;border-radius:50% 50% 2px 2px}.ct-rhythm button.ct-rhythm-step[data-current=true]{outline:3px solid #3977d8;outline-offset:2px}.ct-rhythm-controls{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin-top:24px}.ct-rhythm .ct-rhythm-controls button{min-width:0!important;min-height:38px!important;width:auto!important;height:auto!important;padding:10px 16px!important;border:1px solid #17171255!important;border-radius:2px!important;background:transparent!important;color:#171712!important;font:500 13px/1.2 var(--font-family,system-ui)!important;cursor:pointer}.ct-rhythm .ct-rhythm-controls button[data-transport]{background:#171712!important;color:#f3e4c4!important;border-color:#171712!important;min-width:76px!important}.ct-rhythm-tempo{display:flex!important;align-items:center;gap:8px!important;font-size:12px!important;flex:1!important;min-width:140px!important;margin-left:auto}.ct-rhythm-tempo input{width:100%!important;max-width:120px;min-width:60px;accent-color:#171712;min-height:28px}.ct-rhythm-tempo output{min-width:54px;font-variant-numeric:tabular-nums}.ct-rhythm-compact .ct-rhythm-tempo{flex:1!important;min-width:100px!important;gap:4px!important;margin:0}.ct-rhythm-compact .ct-rhythm-tempo input{min-width:30px!important}.ct-rhythm-compact .ct-rhythm-controls{gap:8px}.ct-rhythm-compact .ct-rhythm-swing{display:none!important}.ct-rhythm-status{font-size:12px;min-height:18px;margin-top:12px;opacity:.7}.ct-rhythm-status:empty{display:none}.ct-rhythm :focus-visible{outline:2px solid #3977d8!important;outline-offset:3px}.ct-rhythm-compact .ct-rhythm-grid{gap:5px;grid-template-columns:repeat(8,minmax(0,1fr))}.ct-rhythm-compact .ct-rhythm-track{display:none}.ct-rhythm-compact button.ct-rhythm-step{border-width:1.5px!important;max-height:38px}.ct-rhythm-compact .ct-rhythm-controls{margin-top:16px;justify-content:center}.ct-rhythm-compact [data-clear],.ct-rhythm-compact [data-shuffle]{display:none!important}.ct-rhythm-compact .ct-rhythm-status{text-align:center}@media(max-width:600px){.ct-rhythm-grid{gap:5px;grid-template-columns:68px repeat(8,minmax(0,1fr))}.ct-rhythm-track{font-size:10px}.ct-rhythm-controls{gap:8px}.ct-rhythm .ct-rhythm-controls button{padding:9px 12px!important}.ct-rhythm:not(.ct-rhythm-compact) .ct-rhythm-tempo{margin-left:0;flex-basis:100%!important}.ct-rhythm-step::after{border-width:1.5px}}
  `;document.head.appendChild(style);
 }
 host.classList.add('ct-rhythm');if(compact)host.classList.add('ct-rhythm-compact');
 const tracks=['Kick','Hat','Bell'],options=[['Kick','Tom','Wood'],['Hat','Shaker','Clap'],['Bell','Pluck','Bass']];let pattern=[[1,0,0,0,1,0,0,0],[0,0,1,0,0,0,1,0],[0,1,0,0,0,1,0,1]],tempo=104,swing=0,playing=false,starting=false,ctx,master,timer,frame,nextTime=0,step=0,queue=[],voices=new Set();
 const grid=document.createElement('div');grid.className='ct-rhythm-grid';grid.setAttribute('role','group');grid.setAttribute('aria-label','Eight-step rhythm pattern');host.appendChild(grid);
 const cells=tracks.map((name,row)=>{
  const label=document.createElement('select');label.className='ct-rhythm-track';label.setAttribute('aria-label',`Track ${row+1} instrument`);for(const name of options[row]){const option=document.createElement('option');option.value=name;option.textContent=name;label.appendChild(option);}label.onchange=()=>{tracks[row]=label.value;cells[row].forEach((b,col)=>b.setAttribute('aria-label',`${label.value}, beat ${col+1}`));};grid.appendChild(label);
  return pattern[row].map((on,col)=>{const button=document.createElement('button');button.type='button';button.className='ct-rhythm-step';button.dataset.track=row;button.setAttribute('aria-label',`${name}, beat ${col+1}`);button.setAttribute('aria-pressed',String(!!on));button.onclick=()=>{pattern[row][col]=1-pattern[row][col];button.setAttribute('aria-pressed',String(!!pattern[row][col]));};grid.appendChild(button);return button;});
 });
 const controls=document.createElement('div');controls.className='ct-rhythm-controls';controls.innerHTML='<button type="button" data-transport aria-label="Play rhythm">Play</button><button type="button" data-shuffle>Shuffle</button><button type="button" data-clear>Clear</button><label class="ct-rhythm-tempo"><input type="range" min="40" max="200" value="104" aria-label="Tempo"><output>104 BPM</output></label><label class="ct-rhythm-tempo ct-rhythm-swing"><span>Swing</span><input type="range" min="0" max="60" value="0" aria-label="Swing"><output>Straight</output></label>';host.appendChild(controls);
 const status=document.createElement('div');status.className='ct-rhythm-status';status.setAttribute('role','status');host.appendChild(status);const transport=controls.querySelector('[data-transport]');
 const stop=()=>{playing=false;clearInterval(timer);cancelAnimationFrame(frame);for(const voice of voices){try{voice.stop()}catch{}}voices.clear();queue=[];if(ctx&&ctx.state==='running')ctx.suspend().catch(()=>{});cells.flat().forEach(b=>delete b.dataset.current);transport.textContent='Play';transport.setAttribute('aria-label','Play rhythm');};
 const hit=(row,time)=>{
  const instrument=tracks[row],gain=ctx.createGain();gain.connect(master);let source,filter;
  const noise=['Hat','Shaker','Clap'].includes(instrument);
  if(noise){
   const length=instrument==='Hat'?.08:instrument==='Shaker'?.16:.22;
   source=ctx.createBufferSource();const buffer=ctx.createBuffer(1,Math.floor(ctx.sampleRate*length),ctx.sampleRate),data=buffer.getChannelData(0);
   for(let i=0;i<data.length;i++)data[i]=Math.random()*2-1;source.buffer=buffer;
   filter=ctx.createBiquadFilter();filter.type=instrument==='Clap'?'bandpass':'highpass';filter.frequency.value=instrument==='Hat'?7000:instrument==='Shaker'?3500:1400;source.connect(filter);filter.connect(gain);
   gain.gain.setValueAtTime(.001,time);
   if(instrument==='Clap'){for(let pulse=0;pulse<3;pulse++){const t=time+pulse*.025;gain.gain.setValueAtTime(.45,t);gain.gain.exponentialRampToValueAtTime(.01,t+.02);}gain.gain.setValueAtTime(.3,time+.075);}
   else gain.gain.linearRampToValueAtTime(instrument==='Hat'?.22:.18,time+.005);
   gain.gain.exponentialRampToValueAtTime(.001,time+length);source.start(time);source.stop(time+length+.01);
  }else{
   const sounds={Kick:[130,45,.18,.65,'sine'],Tom:[210,95,.3,.45,'sine'],Wood:[900,600,.065,.22,'triangle'],Bell:[660,660,.48,.18,'sine'],Pluck:[330,330,.22,.2,'triangle'],Bass:[82.41,82.41,.38,.28,'triangle']};
   const [start,end,length,level,type]=sounds[instrument];source=ctx.createOscillator();source.type=type;source.connect(gain);source.frequency.setValueAtTime(start,time);if(start!==end)source.frequency.exponentialRampToValueAtTime(end,time+length*.7);gain.gain.setValueAtTime(.001,time);gain.gain.exponentialRampToValueAtTime(level,time+.005);gain.gain.exponentialRampToValueAtTime(.001,time+length);source.start(time);source.stop(time+length+.01);
  }
  voices.add(source);source.onended=()=>{voices.delete(source);source.disconnect();filter?.disconnect();gain.disconnect();};
 };
 const schedule=()=>{while(playing&&nextTime<ctx.currentTime+.1){for(let row=0;row<3;row++)if(pattern[row][step])hit(row,nextTime);queue.push({step,time:nextTime});nextTime+=60/tempo/2*(step%2===0?1+swing/100:1-swing/100);step=(step+1)%8;}};
 const paint=()=>{if(!playing)return;if(!host.isConnected||!host.getClientRects().length||document.hidden){stop();return;}while(queue.length&&queue[0].time<=ctx.currentTime){const current=queue.shift().step;cells.forEach(row=>row.forEach((b,i)=>b.dataset.current=String(i===current)));}frame=requestAnimationFrame(paint);};
 transport.onclick=async()=>{
  if(playing){stop();return;}
  if(starting)return;starting=true;
  try{
   const Audio=window.AudioContext||window.webkitAudioContext;if(!Audio)throw Error('Audio is unavailable in this browser.');
   if(!ctx){ctx=new Audio();master=ctx.createGain();master.gain.value=.55;master.connect(ctx.destination);}
   document.dispatchEvent(new CustomEvent('croptop-rhythm-play',{detail:host}));document.querySelectorAll('audio,video').forEach(a=>a.pause());await ctx.resume();
   if(!host.isConnected||!host.getClientRects().length){stop();return;}status.textContent='';playing=true;transport.textContent='Stop';transport.setAttribute('aria-label','Stop rhythm');step=0;nextTime=ctx.currentTime+.025;queue=[];schedule();timer=setInterval(schedule,25);paint();
  }catch(e){status.textContent=e.message||'Unable to play. Try again.';stop();}finally{starting=false;}
 };
 controls.querySelector('[data-shuffle]').onclick=()=>{pattern=pattern.map((row,r)=>row.map((_,i)=>+(Math.random()<(r===0?.35:.25))));if(!pattern.flat().some(Boolean))pattern[0][0]=1;cells.forEach((row,r)=>row.forEach((b,c)=>b.setAttribute('aria-pressed',String(!!pattern[r][c]))));};
 controls.querySelector('[data-clear]').onclick=()=>{pattern=pattern.map(row=>row.map(()=>0));cells.flat().forEach(b=>b.setAttribute('aria-pressed','false'));};
 controls.querySelector('[aria-label=Tempo]').oninput=e=>{tempo=+e.target.value;controls.querySelector('output').textContent=tempo+' BPM';};
 controls.querySelector('[aria-label=Swing]').oninput=e=>{swing=+e.target.value;controls.querySelector('.ct-rhythm-swing output').textContent=swing?swing+'%':'Straight';};
 const other=e=>{if(e.detail!==host)stop();};document.addEventListener('croptop-rhythm-play',other);document.addEventListener('play',stop,true);
 const visibility=()=>{if(document.hidden)stop();};document.addEventListener('visibilitychange',visibility);
 const cleanup=new MutationObserver(()=>{if(host.isConnected)return;stop();ctx?.close().catch(()=>{});document.removeEventListener('croptop-rhythm-play',other);document.removeEventListener('play',stop,true);document.removeEventListener('visibilitychange',visibility);cleanup.disconnect();});cleanup.observe(document.body,{childList:true,subtree:true});
 host.addEventListener('click',e=>e.stopPropagation());host.addEventListener('keydown',e=>e.stopPropagation());
}

 const cfg={"key": "bots", "label": "Bots", "order": 8},small=el.clientHeight<140,ink='#171712',paper='#f3e4c4';
 const backgrounds={start:'#d5b574',writing:'#ead9b6',essays:'#dfc592',documents:'#e4dfcd',audio:'#de7669',video:'#3977d8',studio:'#d59b50',art:'#dfc592',network:'#b4bd99',faq:'#d9b77c',bots:'#dfc592'};
 el.innerHTML='';const box=document.createElement('div');box.className='ct-preview';box.dataset.preview=cfg.key;
 box.style.cssText=`position:absolute;inset:0;background:${backgrounds[cfg.key]};color:${ink};overflow:hidden;`;
 const style=document.createElement('style');style.textContent=`
 .ct-preview *{box-sizing:border-box}.ct-preview .ct-preview-footer{position:absolute;left:24px;right:24px;bottom:18px;display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:36px}
 .ct-preview .ct-preview-label{font:600 17px var(--font-family,system-ui);color:#171712}.ct-preview .ct-preview-choices{display:flex;gap:4px;align-items:center}
 .ct-preview button{display:grid;place-items:center;width:36px;height:36px;padding:0;border:1.5px solid transparent;border-radius:3px 2px 4px 2px;background:transparent;color:#171712;font:16px/1 var(--font-family,system-ui);cursor:pointer}
 .ct-preview button:hover{border-color:#17171288}.ct-preview button[aria-pressed=true]{background:#171712;color:#f3e4c4}.ct-preview button:disabled{opacity:.3;cursor:default}.ct-preview :focus-visible{outline:2px solid #171712;outline-offset:3px}
 .ct-preview input[type=range]{width:84px;accent-color:#171712;cursor:ew-resize}.ct-preview svg{width:16px;height:16px;fill:none;stroke:currentColor;stroke-width:1.5}
 .ct-preview .ct-preview-writing{position:absolute;inset:28px 30px 70px;display:flex;flex-direction:column;justify-content:center;font:16px/1.65 Georgia,serif;letter-spacing:-.02em;color:#171712}.ct-preview .ct-preview-writing strong{font-weight:400;font-size:26px;line-height:1.2;margin-bottom:16px}
 .ct-preview .ct-preview-document span{font:14px/1.5 var(--font-family,system-ui)}.ct-preview .ct-preview-document b{display:block;font-size:11px;margin:14px 0 4px;text-transform:uppercase;letter-spacing:.06em}.ct-preview .ct-preview-essay span{font-size:15px;line-height:1.5}.ct-preview .ct-preview-essay-note{margin-top:12px}.ct-preview{container-type:inline-size}@container(max-width:300px){.ct-preview .ct-preview-writing{inset:18px 20px 58px}.ct-preview .ct-preview-writing strong{font-size:20px;margin-bottom:10px}.ct-preview .ct-preview-essay span,.ct-preview .ct-preview-document span{font-size:12px;line-height:1.4}.ct-preview .ct-preview-essay-note{margin-top:8px}.ct-preview .ct-preview-document b{margin-top:8px}}
 .ct-preview .ct-preview-video{position:absolute;left:8%;top:8%;width:84%;height:66%;object-fit:contain;background:#171712;border:3px solid #171712;border-radius:4px}
 `;box.appendChild(style);
 const texture=document.createElement('canvas');texture.width=texture.height=96;const tx=texture.getContext('2d'),pixels=tx.createImageData(96,96);for(let i=0;i<pixels.data.length;i+=4){const n=Math.sin(i*12.9898)*43758.5453;const dark=n-Math.floor(n)>.5;pixels.data[i]=pixels.data[i+1]=pixels.data[i+2]=dark?0:255;pixels.data[i+3]=Math.round((n-Math.floor(n))*12)}tx.putImageData(pixels,0,0);
 const grain=document.createElement('div');grain.style.cssText=`position:absolute;inset:0;background-image:url(${texture.toDataURL()});pointer-events:none;opacity:.65`;box.appendChild(grain);
 const canvas=document.createElement('canvas');canvas.setAttribute('aria-hidden','true');canvas.style.cssText='position:absolute;inset:0;width:100%;height:100%';box.appendChild(canvas);
 const footer=document.createElement('div');footer.className='ct-preview-footer';const label=document.createElement('span');label.className='ct-preview-label';label.textContent=cfg.label;footer.appendChild(label);box.appendChild(footer);
 if(small){footer.style.cssText='left:10px;right:10px;bottom:5px;min-height:18px';label.style.fontSize='12px'}el.appendChild(box);
 const ctx=canvas.getContext('2d');if(!ctx)return;
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;
 let w=0,h=0,phase=.4,last=0,visible=true,running=!reduced,layout='grid',poster='symbol',density=12,peers=4,media;
 const draw=()=>{
  if(!w||!h)return;ctx.clearRect(0,0,w,h);
  const area=h-(small?22:68),size=Math.min(w*.77,area*.95),x=(w-size)/2,y=(area-size)/2+3;
  if(cfg.key==='studio'||(['writing','essays','documents'].includes(cfg.key)&&!small))return;
  if(cfg.key==='studio'&&poster==='type'){
   ctx.save();ctx.translate(w/2,area/2);ctx.rotate(-.025);ctx.fillStyle=paper;ctx.fillRect(-size*.36,-size*.43,size*.72,size*.86);ctx.strokeStyle=ink;ctx.lineWidth=3;ctx.strokeRect(-size*.36,-size*.43,size*.72,size*.86);ctx.fillStyle=ink;ctx.textAlign='center';ctx.font=`${size*.23}px Georgia,serif`;ctx.fillText('hello.',0,size*.06);ctx.restore();return;
  }
  ctx.save();ctx.translate(x,y);drawCroptopArt(ctx,{kind:cfg.key==='bots'?'art':cfg.key==='art'?'site':cfg.key,width:size,height:size,phase,density,peers,layout,face:cfg.key==='bots'?(density-8+Math.floor(phase*2))%7:null});ctx.restore();
  if(cfg.key==='audio'&&!small){
   const bars=cfg.waveform||[],progress=media&&Number.isFinite(media.duration)?media.currentTime/media.duration:0;
   bars.forEach((v,i)=>{ctx.strokeStyle=i/bars.length<progress?ink:'#17171255';ctx.lineWidth=2;ctx.beginPath();const xx=w*.20+i/bars.length*w*.6,yy=area-1;ctx.moveTo(xx,yy-v*9);ctx.lineTo(xx,yy+v*9);ctx.stroke()});
  }
 };
 const choices=document.createElement('div');choices.className='ct-preview-choices';choices.addEventListener('click',e=>e.stopPropagation());choices.addEventListener('keydown',e=>e.stopPropagation());
 const icons={grid:'<rect x="2" y="2" width="5" height="5"/><rect x="11" y="2" width="5" height="5"/><rect x="2" y="11" width="5" height="5"/><rect x="11" y="11" width="5" height="5"/>',list:'<path d="M2 3h14M2 9h14M2 15h14"/>',symbol:'<path d="M9 1v16M1 9h16M3 3l12 12M3 15L15 3"/>',pause:'<path d="M6 3v12M12 3v12"/>',play:'<path d="m6 3 8 6-8 6z"/>'};
 const setIcon=(b,icon)=>b.innerHTML=`<svg viewBox="0 0 18 18" aria-hidden="true">${icons[icon]}</svg>`;
 const button=(name,content,action)=>{const b=document.createElement('button');b.type='button';b.title=name;b.setAttribute('aria-label',name);if(icons[content])setIcon(b,content);else b.textContent=content;b.onclick=action;choices.appendChild(b);return b;};
 if(cfg.key==='studio'){const instrument=document.createElement('div');instrument.style.cssText='position:absolute;left:24px;right:24px;top:22px;bottom:64px;display:flex;align-items:center';const inner=document.createElement('div');instrument.appendChild(inner);box.appendChild(instrument);mountCroptopRhythm(inner,true);if(small)instrument.style.display='none';}
 if(!small){
  footer.appendChild(choices);
  if(cfg.key==='bots'){
   const slider=document.createElement('input');slider.type='range';slider.min=8;slider.max=18;slider.step=1;slider.value=density;slider.setAttribute('aria-label','Petals');slider.oninput=()=>{density=+slider.value;draw()};choices.appendChild(slider);
   const pause=button(running?'Pause animation':'Play animation',running?'pause':'play',()=>{running=!running;setIcon(pause,running?'pause':'play');pause.title=running?'Pause animation':'Play animation';pause.setAttribute('aria-label',pause.title)});
  }else if(cfg.key==='network'){
   const update=()=>{minus.disabled=peers<=2;plus.disabled=peers>=96;canvas.setAttribute('aria-label',`Illustration: ${peers} peers keeping copies`);draw()};const minus=button('Remove peer','−',()=>{peers=Math.max(2,peers-1);update()});const plus=button('Add peer','+',()=>{peers=Math.min(96,peers+1);update()});update();
  }else if(cfg.key==='essays'){
   const text=document.createElement('div');text.className='ct-preview-writing ct-preview-essay';text.innerHTML='<strong>Croptop TLDR</strong><span>Self-serve websites with content feeds and revenue streams baked in.</span><span class="ct-preview-essay-note">You’re looking at it now. This site is a Croptop site.</span>';box.appendChild(text);
  }else if(cfg.key==='documents'){
   const text=document.createElement('div');text.className='ct-preview-writing ct-preview-document';text.innerHTML='<strong>How Croptop works</strong><span><b>Architecture</b>Local files. A publishing key. A network of copies.<b>Technical guide</b>From your first post to the peers that keep it.</span>';box.appendChild(text);
  }else if(cfg.key==='writing'){
   const text=document.createElement('div');text.className='ct-preview-writing';text.innerHTML='<strong>First bloom</strong><span>The wind nudges<br>each sleeping bud.<br><br>The tree answers<br>in flowers.</span>';box.appendChild(text);
  }else if(cfg.key==='audio'||cfg.key==='video'){
   media=document.createElement(cfg.key);media.src=cfg.media;media.preload='metadata';media.setAttribute('aria-label',cfg.key==='audio'?'Small hours audio sample':'Passing light video sample');
   if(cfg.key==='video'){media.className='ct-preview-video';media.poster=cfg.poster;media.playsInline=true;media.controls=true;media.hidden=true;media.addEventListener('click',e=>e.stopPropagation());}else media.style.display='none';
   const play=button('Play '+cfg.key,'play',async()=>{if(media.paused){try{if(cfg.key==='video')media.hidden=false;await media.play()}catch{play.title='Try playing again';}}else media.pause()});
   const state=()=>{const name=(media.paused?'Play ':'Pause ')+cfg.key;play.title=name;play.setAttribute('aria-label',name);setIcon(play,media.paused?'play':'pause');draw()};
   media.addEventListener('play',()=>{document.querySelectorAll('audio,video').forEach(other=>{if(other!==media)other.pause()});state()});media.addEventListener('pause',state);media.addEventListener('ended',state);media.addEventListener('timeupdate',draw);box.appendChild(media);
  }
 }
 const resize=()=>{const d=Math.min(devicePixelRatio||1,2);w=el.clientWidth;h=el.clientHeight;canvas.width=w*d;canvas.height=h*d;ctx.setTransform(d,0,0,d,0,0);draw()};const ro=new ResizeObserver(resize);ro.observe(el);resize();
 const io=new IntersectionObserver(es=>{visible=es[0].isIntersecting});io.observe(el);
 const animate=t=>{if(!el.isConnected){ro.disconnect();io.disconnect();if(media)media.pause();return}if(running&&visible&&!document.hidden&&t-last>48){phase+=.025;draw();last=t}requestAnimationFrame(animate)};requestAnimationFrame(animate);
}
