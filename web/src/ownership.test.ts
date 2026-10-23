                                                                                         
                   
                                                                                       
                                                                                                  
                                                                                                
                                                               
                                               
   
                            
                                                                  
                                                                             
import {expect,it} from 'vitest';
import {mergeReportPage} from './ownership';
import type {RunReport} from './types';

                                                                                                 
                                                                                
                                                                                   
                
   
it('advances only the requested dimension beyond 600 chart points',()=>{
 const page=(from:number,sampleOffset:number)=>({id:'r',revision:'9007199254740993',cut:'same',population:'all',templateId:'t',points:Array.from({length:600},(_,i)=>({index:String(from+i)})),samples:Array.from({length:100},(_,i)=>({RunID:'r',TemplateID:'t',Sequence:sampleOffset+i,Attempt:1})),range:{fromIndex:String(from),nextIndex:from===1200?null:String(from+600),totalBuckets:'1800',totalSamples:'300',nextSampleOffset:sampleOffset===200?null:sampleOffset+100}} as unknown as RunReport);
 let report=page(0,0);
 report=mergeReportPage(report,page(600,0),'chart').report;
 expect([report.range.nextIndex,report.range.nextSampleOffset]).toEqual(['1200',100]);
 report=mergeReportPage(report,page(0,100),'samples').report;
 expect([report.range.nextIndex,report.range.nextSampleOffset]).toEqual(['1200',200]);
 expect(report.points).toHaveLength(1200);
 report=mergeReportPage(report,page(1200,0),'chart').report;
 expect([report.range.nextIndex,report.range.nextSampleOffset]).toEqual([null,200]);
 report=mergeReportPage(report,page(0,200),'samples').report;
 expect([report.range.nextIndex,report.range.nextSampleOffset,report.range.fromIndex]).toEqual([null,null,'0']);
 expect(report.points.map(p=>p.index)).toEqual(Array.from({length:1800},(_,i)=>String(i)));
 expect(report.samples.map(s=>s.Sequence)).toEqual(Array.from({length:300},(_,i)=>i));
 console.info('Interleaved pages retain exact independent cursors and initial chart range');
});
                                  

                                                                                            
                                                  
                                                                            
                
   
it('replaces both dimensions when revision, cut, scope, population or run changes',()=>{
 const old={id:'r',revision:'1',cut:'same',population:'all',templateId:'t',points:[],samples:[],range:{}} as unknown as RunReport;
 for(const change of [{revision:'2'},{cut:'new'},{templateId:'u'},{population:'success'},{id:'other'}]){
  const next={...old,...change};
  expect(mergeReportPage(old,next,'samples')).toEqual({report:next,replaced:true});
 }
});
                                 
