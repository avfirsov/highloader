                                                                                         
                   
                                                                       
                                                                                   
                                                                            
                             
   
                            
                                                               
                                                                 
import {useState} from 'react';
import type {RunReport} from './types';
                                                                                             
                                                                   
                                                                
                
   
export function ComparisonSetup({runs,onCompare,disabled}:{runs:[RunReport,RunReport];onCompare:(mapping:Record<string,string>)=>void;disabled:boolean}){const [mapping,setMapping]=useState<Record<string,string>>({});const left=runs[0].snapshot.draft.templates,right=runs[1].snapshot.draft.templates;return <div><h3>Сопоставление шаблонов</h3><p>{runs[0].id} → {runs[1].id}. Выберите пары явно. Пустая пара не связывает разные идентификаторы по имени файла.</p><table><tbody>{left.map(t=><tr key={t.id}><td>{t.filename} · {t.id} · {t.digest.slice(0,12)}</td><td><select aria-label={`Пара для ${t.filename}`} value={mapping[t.id]||''} onChange={e=>{const next={...mapping};if(e.target.value)next[t.id]=e.target.value;else delete next[t.id];setMapping(next);}}><option value="">Без явной пары</option>{right.map(r=><option key={r.id} value={r.id} disabled={Object.entries(mapping).some(([id,target])=>id!==t.id&&target===r.id)}>{r.filename} · {r.id} · {r.digest.slice(0,12)}</option>)}</select></td></tr>)}</tbody></table><button disabled={disabled} onClick={()=>onCompare(mapping)}>Построить сравнение</button></div>;}
                                 
