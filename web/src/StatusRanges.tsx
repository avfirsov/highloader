                                                                                 
                   
                                                                       
                                                                      
                                                                    
                                         
   
                            
                                                         
                                                
import {useEffect,useState} from 'react';
import type {Policy} from './types';
                                                                                            
                                                  
                                                           
                
   
export function parseStatuses(text:string):Policy['statuses']{if(!text.trim())return [];return text.split(',').map(part=>{const m=/^\s*(\d{3})(?:\s*-\s*(\d{3}))?\s*$/.exec(part);if(!m)throw new Error('Введите статусы или диапазоны: 429, 500-599');const from=Number(m[1]),to=Number(m[2]||m[1]);if(from<100||to>599||to<from)throw new Error('Диапазон должен быть от 100 до 599, по возрастанию');return {from,to};});}
                               
                                                                                   
                                                  
                                                                              
                
   
export function StatusRanges({value,disabled,onChange}:{value:Policy['statuses'];disabled:boolean;onChange:(v:Policy['statuses'])=>void}){const formatted=(value||[]).map(r=>r.from===r.to?String(r.from):`${r.from}-${r.to}`).join(', ');const [text,setText]=useState(formatted),[error,setError]=useState('');useEffect(()=>{setText(formatted);setError('');},[formatted,disabled]);const commit=()=>{try{const ranges=parseStatuses(text);setError('');onChange(ranges);}catch(e){setError(String((e as Error).message));}};return <><input aria-invalid={!!error} disabled={disabled} value={text} onChange={e=>setText(e.target.value)} onBlur={commit} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();commit();}}}/>{error&&<small role="alert">{error}</small>}</>;}
                              
