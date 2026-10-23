                                                                                 
                   
                                                                       
                                                                                  
                                                                                                                
                                                                                                        
                    
   
                            
                                           
                                                      
                                                                              
                                               
                                                           
   
export class Client {
 token='';epoch='';private connection=0;
 // Calling an unbound native fetch as this.transport supplies Client as its receiver; bind only the default, preserving injected transports.
 constructor(private transport:typeof fetch=globalThis.fetch.bind(globalThis)){}
 async connect(signal?:AbortSignal){const connection=++this.connection;const s=await this.request<{csrfToken:string;epoch:string}>('/session','GET',undefined,signal);if(connection===this.connection){this.token=s.csrfToken;this.epoch=s.epoch;}return s;}
 async response(path:string,method='GET',body?:unknown,signal?:AbortSignal){const r=await this.transport('/api/v1'+path,{method,credentials:'same-origin',signal,headers:{...(body!==undefined?{'Content-Type':'application/json'}:{}),...(method!=='GET'?{'X-CSRF-Token':this.token}:{})},body:body===undefined?undefined:JSON.stringify(body)});if(!r.ok){let message=`Ошибка ${r.status}`;try{const data=await r.json();message=data.message||data.error||message;}catch{/* A non-JSON failure still retains its status. */}throw new Error(message);}return r;}
 async request<T>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>{const r=await this.response(path,method,body,signal);return r.status===204?undefined as T:r.json() as Promise<T>;}
 async download(body:unknown){const r=await this.response('/export','POST',body);const url=URL.createObjectURL(await r.blob());const a=document.createElement('a');a.href=url;a.download='highloader-report.html';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
}
                         
