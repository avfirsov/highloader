                                                                                        
                   
                                                                    
                                                
                                                                            
                          
   
                            
                                              
                                               
import { defineConfig } from 'vitest/config';
export default defineConfig({test:{reporters:['default'],environment:'jsdom',coverage:{provider:'v8',include:['src/**/*.{ts,tsx}'],exclude:['src/**/*.test.{ts,tsx}','src/types.ts'],reporter:['text','json','json-summary']}}});

