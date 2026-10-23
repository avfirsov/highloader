                                                                                              
                   
                                                                                      
                                                                                              
                                                                                    
                                                                                              
                                        
   
                            
                                                                    
                                                                          
import type {RunReport,View} from './types';
import {newer} from './state';
export interface Ticket {generation:number;session:string;runId:string|null}
                                                                                          
                                                                  
                                                                              
   
export class LiveOwner {
 generation=0;session='';view:View|null=null;
 ticket():Ticket{return {generation:this.generation,session:this.session,runId:this.view?.runId??null};}
 invalidate(){this.generation++;}
 reconnect(session:string,view:View|null){this.generation++;this.session=session;this.view=view;}
 start(view:View){this.generation++;this.view=view;}
 accept(ticket:Ticket,next:View|null,allowRunChange=false):boolean {
  if(ticket.generation!==this.generation||ticket.session!==this.session)return false;
  if(!next)return !this.view;
  if(this.view?.runId!==next.runId){if(!allowRunChange)return false;if(ticket.runId!==(this.view?.runId??null))return false;this.generation++;this.view=next;return true;}
  if(!newer(this.view,next))return false;this.view=next;return true;
 }
}
                            
                                                                                                     
                                                                                                 
                                                                    
                
   
export function mergeReportPage(old:RunReport,next:RunReport,dimension:'chart'|'samples'='chart'):{report:RunReport;replaced:boolean}{
 const same=old.id===next.id&&old.revision===next.revision&&old.cut===next.cut&&old.population===next.population&&old.templateId===next.templateId;
 if(!same)return {report:next,replaced:true};
 // the server also returns the other dimension's default page; it owns no continuation here.
 const points=dimension==='chart'?Array.from(new Map([...old.points,...next.points].map(p=>[p.index,p])).values()):old.points;
 const sampleKey=(s:Record<string,unknown>)=>[s.RunID,s.TemplateID,s.LogicalID,s.Sequence,s.Attempt].map(String).join('|');
 const samples=dimension==='samples'?Array.from(new Map([...(old.samples||[]),...(next.samples||[])].map(s=>[sampleKey(s),s])).values()):old.samples;
 const range=dimension==='chart'?{...next.range,fromIndex:old.range.fromIndex,totalSamples:old.range.totalSamples,nextSampleOffset:old.range.nextSampleOffset}:{...old.range,totalSamples:next.range.totalSamples,nextSampleOffset:next.range.nextSampleOffset};
 return {report:{...next,range,points,samples},replaced:false};
}
                                 
