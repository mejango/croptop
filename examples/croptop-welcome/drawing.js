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
