                                                                                 
                   
                                                                       
                                                                        
                                                                                                     
                                                                                              
                                  
   
                            
                                            
                                                         
import {defineConfig} from 'vite';
// Library mode leaves React's process.env.NODE_ENV intact; standalone browsers have no process global.
export default defineConfig({define:{'process.env.NODE_ENV':JSON.stringify('production')},build:{outDir:'report-dist',emptyOutDir:true,cssCodeSplit:false,sourcemap:false,lib:{entry:'src/report/offline.tsx',name:'HighloaderReport',formats:['iife'],fileName:()=> 'report.js',cssFileName:'report'},rollupOptions:{output:{inlineDynamicImports:true}}}});
