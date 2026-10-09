(() => {
 const mount = () => document.querySelectorAll('.ct-download:not([data-ready])').forEach(root => {
  root.dataset.ready='true';
  const latest='https://github.com/mejango/croptop/releases/latest/download/';
  const linuxAsset=(format,arch)=>`https://github.com/mejango/croptop/releases/download/v0.11.0/croptop_0.11.0_linux_${arch}.${format}`;
  root.querySelectorAll('.ct-download-card').forEach(card=>{
   let arch='amd64',format='deb';
   const update=()=>{
    const os=card.dataset.os,link=card.querySelector('.ct-download-link');
    link.href=os==='mac'?latest+'Croptop.dmg':os==='windows'?latest+`croptop-setup-${arch}.exe`:linuxAsset(format,arch);
    link.setAttribute('aria-label',`Download for ${os==='mac'?'Mac, Apple Silicon and Intel':os==='windows'?`Windows, ${arch==='amd64'?'Intel and AMD':'ARM'}`:`Linux, ${format}, ${arch==='amd64'?'Intel and AMD':'ARM'}`}`);
    card.querySelectorAll('[data-arch]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.arch===arch)));
    card.querySelectorAll('[data-format]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.format===format)));
    if(os==='linux')card.querySelector('.ct-download-note').textContent=format==='deb'?'Ubuntu / Debian':'Fedora / Red Hat';
   };
   card.querySelectorAll('[data-arch]').forEach(b=>b.onclick=()=>{arch=b.dataset.arch;update()});
   card.querySelectorAll('[data-format]').forEach(b=>b.onclick=()=>{format=b.dataset.format;update()});update();
   const c=card.querySelector('canvas'),x=c.getContext('2d');if(!x)return;
   x.lineCap='round';x.lineJoin='round';x.strokeStyle='#171712';x.fillStyle='#f3e4c4';x.lineWidth=6;
   const path=(points,fill=false)=>{x.beginPath();points.forEach(([a,b],i)=>i?x.lineTo(a,b):x.moveTo(a,b));if(fill){x.closePath();x.fill()}x.stroke()};
   if(card.dataset.os==='mac'){
    path([[43,35],[215,38],[211,142],[45,140]],true);path([[45,141],[23,166],[236,169],[213,143]],true);path([[104,157],[158,158]]);
    x.save();x.translate(91,53);x.scale(.42,.42);x.lineWidth=7;path([[25,20],[31,172]]);path([[30,112],[0,87],[4,122],[31,141]],true);path([[30,102],[60,80],[55,116],[31,131]],true);x.beginPath();for(let i=0;i<80;i++){const a=i/80*Math.PI*2,r=29+9*Math.cos(10*a);const px=28+Math.cos(a)*r,py=42+Math.sin(a)*r;i?x.lineTo(px,py):x.moveTo(px,py)}x.closePath();x.fill();x.stroke();x.beginPath();x.arc(28,42,14,0,Math.PI*2);x.fill();x.stroke();x.restore();
   }else if(card.dataset.os==='linux'){
    path([[34,30],[227,34],[224,160],[36,162]],true);path([[36,56],[225,59]]);path([[62,82],[81,101],[62,120]]);path([[101,122],[140,123]]);
   }else{
    path([[43,29],[218,32],[215,153],[41,157]],true);path([[128,33],[130,153]]);path([[44,91],[216,89]]);path([[130,157],[130,173]]);path([[94,175],[168,176]]);
   }
  });
 });
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',mount,{once:true});else mount();
})();
