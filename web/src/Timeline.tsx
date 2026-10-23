                                                                                 
                   
                                                                                         
                                                                            
                                                                             
                      
   
                            
                                                            
                                                                        
import type {Profile} from './types';
import {moveStep} from './state';
import {NumberField} from './NumberField';
                                                                            
                                                  
                                                                       
                
   
export function Timeline({profile,onChange,disabled=false}:{profile:Profile;onChange:(p:Profile)=>void;disabled?:boolean}){
 const total=profile.steps.reduce((a,s)=>a+s.durationSeconds,0)||1,max=Math.max(10,...profile.steps.map(s=>s.level))*1.2;
 let elapsed=0;const segments=profile.steps.map((s,i)=>{const x=40+elapsed/total*900;elapsed+=s.durationSeconds;return {s,i,x,end:40+elapsed/total*900,y:180-s.level/max*150};});
 const edit=(i:number,k:'level'|'durationSeconds',v:number)=>{if(!disabled)onChange({...profile,steps:moveStep(profile.steps,i,k,v)});};
 return <section><h3>Профиль нагрузки</h3><p>Перетащите точку вверх/вниз для уровня, вправо/влево для длительности. Стрелки: уровень ↑↓, длительность ←→.</p><svg className="timeline" viewBox="0 0 1000 210" aria-label="Ступенчатый профиль нагрузки">{segments.map(({s,i,x,end,y})=><g key={i}><path d={`M${x} 180V${y}H${end}V180Z`} fill="var(--tint)" stroke="var(--accent)"/><text x={x+6} y={Math.max(15,y-8)}>{s.level} · {s.durationSeconds} с</text><circle cx={end} cy={y} r="8" tabIndex={disabled?-1:0} aria-disabled={disabled} role="slider" aria-label={`Ступень ${i+1}`} aria-valuenow={s.level} aria-valuemin={0} onKeyDown={e=>{if(!disabled&&['ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(e.key)){e.preventDefault();edit(i,e.key==='ArrowUp'||e.key==='ArrowDown'?'level':'durationSeconds',e.key==='ArrowUp'?s.level+1:e.key==='ArrowDown'?s.level-1:e.key==='ArrowRight'?s.durationSeconds+1:s.durationSeconds-1);}}} onPointerDown={e=>{if(!disabled)e.currentTarget.setPointerCapture(e.pointerId);}} onPointerMove={e=>{if(disabled||!e.currentTarget.hasPointerCapture(e.pointerId))return;const box=e.currentTarget.ownerSVGElement!.getBoundingClientRect();const yv=(e.clientY-box.top)/box.height*210;const xv=(e.clientX-box.left)/box.width*1000;const steps=moveStep(moveStep(profile.steps,i,'level',(180-yv)/150*max),i,'durationSeconds',(xv-x)/900*total);onChange({...profile,steps});}} onPointerUp={e=>{if(e.currentTarget.hasPointerCapture(e.pointerId))e.currentTarget.releasePointerCapture(e.pointerId);}}/></g>)}</svg><div className="stages">{profile.steps.map((s,i)=><div key={i}><strong>Ступень {i+1}</strong><label>Длительность, с<NumberField min={1} disabled={disabled} value={s.durationSeconds} onChange={n=>edit(i,'durationSeconds',n)}/></label><label>{profile.mode==='open'?'Запросов / с':'Пользователей'}<NumberField disabled={disabled} value={s.level} onChange={n=>edit(i,'level',n)}/></label><button disabled={disabled||profile.steps.length===1} onClick={()=>onChange({...profile,steps:profile.steps.filter((_,n)=>n!==i)})}>Удалить ступень</button></div>)}</div><button disabled={disabled} onClick={()=>onChange({...profile,steps:[...profile.steps,{durationSeconds:30,level:100}]})}>Добавить ступень</button></section>;
}
                          

