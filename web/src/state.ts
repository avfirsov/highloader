                                                                                             
                   
                                                                       
                                                                                     
                                                                
                                                            
   
                            
                                                        
                                                            
import type {Draft, EditableDraft, Step, View} from './types';
                                                                                              
                                          
                                                            
                             
                
   
export function editable(d:Draft):EditableDraft {const {selectedIds,settings,url,method,auth,headers,policy,profile,selectionMode,capacity,samples}=d;return {selectedIds,settings,url,method,auth,headers,policy,profile,selectionMode,capacity,samples};}
                          
                                                                                          
                                                      
                                                                   
                
   
export function moveStep(steps:Step[],index:number,key:keyof Step,value:number):Step[] {if(!Number.isSafeInteger(Math.round(value)))return steps;return steps.map((s,i)=>i===index?{...s,[key]:Math.max(key==='durationSeconds'?1:0,Math.round(value))}:s);}
                          
                                                                                    
                                                
                                                      
                
   
export function decodeRaw(raw:string):string {try{return new TextDecoder().decode(Uint8Array.from(atob(raw),c=>c.charCodeAt(0)));}catch{return 'Исходные данные недоступны';}}
                           
                                                                              
                                       
                                                                        
                
   
export function newer(old:View|null,next:View):boolean {return !old||old.runId!==next.runId||BigInt(next.revision)>=BigInt(old.revision);}
                       
                                                                                     
                                                   
                                                                         
                
   
export function terminal(v:View|null):boolean {return !v||v.workersReleased&&v.writerReleased!==false&&v.durable&&!v.persistenceError&&!['running','stopping','draining','starting','finalizing'].includes(v.state);}
                          
