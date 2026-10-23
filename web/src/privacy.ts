                                                                                          
                   
                                                                                         
                                                                      
                                                                     
                                   
   
                            
                                                             
                                                                  
                                                                                               
                                                                
                                                                         
                
   
export function secretHeader(name:string,apiKey=''):boolean{return /authorization|cookie|api[-_]?key|token|secret/i.test(name)||!!apiKey&&name.toLowerCase()===apiKey.toLowerCase();}
                              
                                                                                  
                                                   
                                                                                       
                
   
export function maskURL(value:string):string{try{const u=new URL(value);if(u.username||u.password){u.username='•••';u.password='•••';}for(const key of Array.from(u.searchParams.keys()))u.searchParams.set(key,'•••');return u.toString();}catch{return value.includes('?')?value.slice(0,value.indexOf('?'))+'?•••':value.replace(/\/\/[^/]*@/,'//•••@');}}
                         
