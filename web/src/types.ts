                                                                                                  
                   
                                                                                   
                                                  
                                  
                                 
                                                                                    
                                                                                                       
                                                               
                                                               
   
                            
                                                               
                                                                   
export type Mode = 'open'|'closed';
export interface Header {enabled:boolean; name:string; value:string; suppress:boolean}
export interface Policy {timeoutMs:number; maxRetries:number; statuses:{from:number;to:number}[];network:boolean;timeouts:boolean;delayMode:string;delayMs:number;maxDelayMs:number;jitter:boolean;deadlineMs:number}
export interface Step {durationSeconds:number;level:number}
export interface Profile {mode:Mode;steps:Step[];retryAccounting:'within'|'above'}
export interface Auth {mode:string;token:string;username:string;password:string;name:string;value:string;placement:string}
export interface Settings {method?:string|null;headers:Header[];policy:Partial<Policy>;weight:number}
export interface Source {id:string;filename:string;raw:string;digest:string;headers:{name:string;value:string|null}[];diagnostics:Diagnostic[];valid:boolean;importOrder:number;body?:unknown}
export interface Diagnostic {field:string;message:string;offset?:number}
export interface EditableDraft {selectedIds:string[];settings:Record<string,Settings>;url:string;method:string;auth:Auth;headers:Header[];policy:Policy;profile:Profile;selectionMode:string;capacity:number;samples:{success:number;failure:number;bodyCapBytes:number}}
export interface Draft extends EditableDraft {schemaVersion:number;revision:string;templates:Source[]}
export interface Effective {templateId:string;url:string;method:string;methodSource:string;headers:{name:string;value:string;deleted:boolean;source:string;locked:boolean}[];policy:Policy;policySources:Record<string,string>;weight:number}
export interface Preflight {effective:Effective[];resources:Record<string,number>;diagnostics:Diagnostic[]}
export interface Aggregate {initial:string;retries:string;started:string;statuses:Record<string,string>;attempts:string[];logical:string[];networkFailures:string;attemptTimeouts:string;deadlineInterruptions:string;bodyReadFailures:string}
export interface View {mode:Mode;retryAccounting:Profile['retryAccounting'];writerReleased?:boolean;cpuStats?:{valid:string;missing:string;sum:number;min:number|null;max:number|null};completedRate?:number|null;targetUsers?:string|null;revision:string;runId:string;state:string;reason:string;elapsedMs:number;remainingMs:number;stage:number;desiredRate:number|null;initialRate:number;retryRate:number;totalRate:number;shortfall:number|null;aggregate:Aggregate;desired:string;schedulerMisses:string;capacityMisses:string;unfinished:number;capacity:number;activeUsers:number;retiringUsers:number;cpu:number|null;cpuReason:string;durableSequence:string;durableCut:string;persistenceError:string;durable:boolean;workersReleased:boolean;points:unknown[]}
export interface Active {closing?:boolean;runId:string|null;view:View|null}
export interface Latency {count:string;minMs:number|null;meanMs:number|null;maxMs:number|null;p50Ms:number|null;p90Ms:number|null;p95Ms:number|null;p99Ms:number|null;resolutionLimited:string}
export interface Summary {measurementSeconds?:number|null;initialRate?:number|null;retryRate?:number|null;totalRate?:number|null;cpu?:{count:string;missing?:string;minPercent:number|null;meanPercent:number|null;maxPercent:number|null};initial:string;retries:string;attempts:string;logicalStarted:string;logicalSuccess:string;logicalFailure:string;logicalDeadline:string;logicalCanceled:string;logicalUnknown:string;httpErrors:string;technicalErrors:string;failedAttempts:string;canceledAttempts:string;unknownAttempts:string;schedulerMisses:string;capacityMisses:string;desired:string;statuses:Record<string,string>;attemptLatency:Latency;logicalLatency:Latency}
export interface ChartPoint {completedCount?:string;measuredFinished?:string;canceledCount?:string;unknownCount?:string;cpuReason?:string;intervalKnown?:boolean;basePresent?:boolean;completedRate?:number|null;targetUsers?:string|null;activeUsers?:string|null;retiringUsers?:string|null;index:string;startMs:number;endMs:number;desiredRate:number|null;initialRate:number|null;retryRate:number|null;totalRate:number|null;shortfall:number|null;errorCount:string|null;p50Ms:number|null;p90Ms:number|null;p95Ms:number|null;p99Ms:number|null;cpu:number|null}
export interface RunReport {revision?:string;durableRevision?:string;logicalPopulationNote?:string;population?:string;templateId?:string;chartScopes?:Record<string,ChartPoint[]>;schemaVersion:1;id:string;snapshot:{runId:string;createdAt:string;appVersion:string;draft:Draft;effective:Effective[];resources:Record<string,number>};state:string;reason:string;cut:string;durableCut:string|null;durable:boolean;recovery:unknown;summary:Summary;templates:Dimension[];stages:Dimension[];points:ChartPoint[];samples:Record<string,unknown>[];range:{selection?:string;volatileAdditionalKeys?:string;fromIndex:string;nextIndex:string|null;totalBuckets:string;downsampled:boolean;totalSamples?:string;nextSampleOffset?:number|null}}
export interface Dimension {kind:string;templateId?:string;stage:number;label:string;summary:Summary}
export interface Comparison {schemaVersion:1;runs:[RunReport,RunReport];differences:{path:string;left:unknown;right:unknown}[];deltas:{key:string;before:string|number;after:string|number;absolute:string|number;percent:number|null}[];alignment:'elapsed'}
export type Report = RunReport|Comparison;
export interface HistoryItem {url?:string;mode?:string;templateCount?:number;latestSequence?:string;durableCut?:string;counts?:{initial:string;attempts:string;errors:string|null}|null;id:string;createdAt:string;state:string;reason:string}



