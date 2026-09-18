export namespace main {
	
	export class AskItem {
	    questionId?: string;
	    selected?: string[];
	    text?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.questionId = source["questionId"];
	        this.selected = source["selected"];
	        this.text = source["text"];
	    }
	}
	export class AskAnswer {
	    id: string;
	    sessionId?: string;
	    answers: AskItem[];
	    cancelled?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AskAnswer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.answers = this.convertValues(source["answers"], AskItem);
	        this.cancelled = source["cancelled"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class AskOption {
	    label: string;
	    description?: string;
	
	    static createFrom(source: any = {}) {
	        return new AskOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.description = source["description"];
	    }
	}
	export class AskQuestion {
	    id?: string;
	    header?: string;
	    question: string;
	    options?: AskOption[];
	    multiSelect?: boolean;
	    allowFreeText: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AskQuestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.header = source["header"];
	        this.question = source["question"];
	        this.options = this.convertValues(source["options"], AskOption);
	        this.multiSelect = source["multiSelect"];
	        this.allowFreeText = source["allowFreeText"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AskRequest {
	    id: string;
	    sessionId: string;
	    questions: AskQuestion[];
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new AskRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.questions = this.convertValues(source["questions"], AskQuestion);
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AuditEntry {
	    time: number;
	    sessionId: string;
	    tool: string;
	    subject: string;
	    decision: string;
	    stage: string;
	    reason: string;
	    rule?: string;
	    source?: string;
	
	    static createFrom(source: any = {}) {
	        return new AuditEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.sessionId = source["sessionId"];
	        this.tool = source["tool"];
	        this.subject = source["subject"];
	        this.decision = source["decision"];
	        this.stage = source["stage"];
	        this.reason = source["reason"];
	        this.rule = source["rule"];
	        this.source = source["source"];
	    }
	}
	export class CLIConfig {
	    command: string;
	    timeout?: number;
	
	    static createFrom(source: any = {}) {
	        return new CLIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.timeout = source["timeout"];
	    }
	}
	export class ContextStat {
	    sessionId: string;
	    usedTokens: number;
	    windowTokens: number;
	    ratio: number;
	    messageCount: number;
	    coveredMsgs: number;
	    summaryChars: number;
	    summaryAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new ContextStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.usedTokens = source["usedTokens"];
	        this.windowTokens = source["windowTokens"];
	        this.ratio = source["ratio"];
	        this.messageCount = source["messageCount"];
	        this.coveredMsgs = source["coveredMsgs"];
	        this.summaryChars = source["summaryChars"];
	        this.summaryAt = source["summaryAt"];
	    }
	}
	export class PlanStep {
	    index: number;
	    title: string;
	    detail?: string;
	    status: string;
	    summary?: string;
	    error?: string;
	    startedAt?: number;
	    finishedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new PlanStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.status = source["status"];
	        this.summary = source["summary"];
	        this.error = source["error"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	    }
	}
	export class Plan {
	    id: string;
	    sessionId: string;
	    title: string;
	    status: string;
	    steps: PlanStep[];
	    createdAt: number;
	    updatedAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Plan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.title = source["title"];
	        this.status = source["status"];
	        this.steps = this.convertValues(source["steps"], PlanStep);
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffLine {
	    type: string;
	    oldLineNo: number;
	    newLineNo: number;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.oldLineNo = source["oldLineNo"];
	        this.newLineNo = source["newLineNo"];
	        this.content = source["content"];
	    }
	}
	export class DiffHunk {
	    header: string;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new DiffHunk(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.header = source["header"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DiffFile {
	    path: string;
	    oldPath?: string;
	    status: string;
	    additions: number;
	    deletions: number;
	    hunks: DiffHunk[];
	
	    static createFrom(source: any = {}) {
	        return new DiffFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.oldPath = source["oldPath"];
	        this.status = source["status"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.hunks = this.convertValues(source["hunks"], DiffHunk);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Message {
	    id: string;
	    role: string;
	    content: string;
	    toolCalls?: ToolCall[];
	    toolCallId?: string;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.toolCallId = source["toolCallId"];
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolCall {
	    id: string;
	    name: string;
	    args?: Record<string, any>;
	    status: string;
	    duration?: number;
	    result?: string;
	    files?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.args = source["args"];
	        this.status = source["status"];
	        this.duration = source["duration"];
	        this.result = source["result"];
	        this.files = source["files"];
	    }
	}
	export class ChatResult {
	    reply: string;
	    toolCalls?: ToolCall[];
	    messages?: Message[];
	    diff?: DiffFile[];
	    plan?: Plan;
	    error?: string;
	    cancelled?: boolean;
	    cancelKind?: string;
	    context?: ContextStat;
	
	    static createFrom(source: any = {}) {
	        return new ChatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reply = source["reply"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.diff = this.convertValues(source["diff"], DiffFile);
	        this.plan = this.convertValues(source["plan"], Plan);
	        this.error = source["error"];
	        this.cancelled = source["cancelled"];
	        this.cancelKind = source["cancelKind"];
	        this.context = this.convertValues(source["context"], ContextStat);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CheckpointInfo {
	    turn: number;
	    label: string;
	    available: boolean;
	    reason?: string;
	    files: string[];
	    conflicts: string[];
	    undone?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CheckpointInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn = source["turn"];
	        this.label = source["label"];
	        this.available = source["available"];
	        this.reason = source["reason"];
	        this.files = source["files"];
	        this.conflicts = source["conflicts"];
	        this.undone = source["undone"];
	    }
	}
	
	export class Conversation {
	    index: number;
	    query: string;
	    answer: string;
	    startTime: number;
	    endTime: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.query = source["query"];
	        this.answer = source["answer"];
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.error = source["error"];
	    }
	}
	
	
	
	export class DiffTurn {
	    turn: number;
	    label: string;
	    files: DiffFile[];
	    additions: number;
	    deletions: number;
	    createdAt: number;
	    base?: string;
	    endState?: Record<string, string>;
	    undone?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DiffTurn(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn = source["turn"];
	        this.label = source["label"];
	        this.files = this.convertValues(source["files"], DiffFile);
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	        this.createdAt = source["createdAt"];
	        this.base = source["base"];
	        this.endState = source["endState"];
	        this.undone = source["undone"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Grant {
	    tool: string;
	    spec?: string;
	    isPrefix?: boolean;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new Grant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = source["tool"];
	        this.spec = source["spec"];
	        this.isPrefix = source["isPrefix"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class HTTPConfig {
	    method?: string;
	    url: string;
	    headers?: Record<string, string>;
	    body?: string;
	    timeout?: number;
	
	    static createFrom(source: any = {}) {
	        return new HTTPConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.url = source["url"];
	        this.headers = source["headers"];
	        this.body = source["body"];
	        this.timeout = source["timeout"];
	    }
	}
	export class LLMToolFunction {
	    name: string;
	    arguments: string;
	
	    static createFrom(source: any = {}) {
	        return new LLMToolFunction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.arguments = source["arguments"];
	    }
	}
	export class LLMToolCall {
	    id: string;
	    type: string;
	    function: LLMToolFunction;
	
	    static createFrom(source: any = {}) {
	        return new LLMToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.function = this.convertValues(source["function"], LLMToolFunction);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LLMMessage {
	    role: string;
	    content: string;
	    tool_call_id?: string;
	    tool_calls?: LLMToolCall[];
	
	    static createFrom(source: any = {}) {
	        return new LLMMessage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = source["content"];
	        this.tool_call_id = source["tool_call_id"];
	        this.tool_calls = this.convertValues(source["tool_calls"], LLMToolCall);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LLMRequestSnapshot {
	    sessionId: string;
	    runId?: string;
	    turn: number;
	    model: string;
	    at: number;
	    systemPrompt: string;
	    messages: LLMMessage[];
	    summaryIndex: number;
	    toolNames: string[];
	    toolSchemaTokens: number;
	    estimatedTokens: number;
	    windowTokens: number;
	    compactedThisTurn: boolean;
	    coveredMsgs: number;
	
	    static createFrom(source: any = {}) {
	        return new LLMRequestSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.runId = source["runId"];
	        this.turn = source["turn"];
	        this.model = source["model"];
	        this.at = source["at"];
	        this.systemPrompt = source["systemPrompt"];
	        this.messages = this.convertValues(source["messages"], LLMMessage);
	        this.summaryIndex = source["summaryIndex"];
	        this.toolNames = source["toolNames"];
	        this.toolSchemaTokens = source["toolSchemaTokens"];
	        this.estimatedTokens = source["estimatedTokens"];
	        this.windowTokens = source["windowTokens"];
	        this.compactedThisTurn = source["compactedThisTurn"];
	        this.coveredMsgs = source["coveredMsgs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class MCPConfig {
	    type?: string;
	    url?: string;
	    headers?: Record<string, string>;
	    command?: string;
	    args?: string[];
	    env?: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new MCPConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.url = source["url"];
	        this.headers = source["headers"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	    }
	}
	export class MCPImportSkip {
	    name: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPImportSkip(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.reason = source["reason"];
	    }
	}
	export class MCPImportResult {
	    imported: string[];
	    skipped: MCPImportSkip[];
	
	    static createFrom(source: any = {}) {
	        return new MCPImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imported = source["imported"];
	        this.skipped = this.convertValues(source["skipped"], MCPImportSkip);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class MCPToolMeta {
	    name: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPToolMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	    }
	}
	
	export class Model {
	    name: string;
	    alias: string;
	    modelId: string;
	    apiKey: string;
	    url: string;
	    protocol?: string;
	    contextWindow?: number;
	
	    static createFrom(source: any = {}) {
	        return new Model(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.alias = source["alias"];
	        this.modelId = source["modelId"];
	        this.apiKey = source["apiKey"];
	        this.url = source["url"];
	        this.protocol = source["protocol"];
	        this.contextWindow = source["contextWindow"];
	    }
	}
	export class PermissionAskRequest {
	    id: string;
	    sessionId: string;
	    tool: string;
	    subject: string;
	    units: string[];
	    stage: string;
	    reason: string;
	    createdAt: number;
	
	    static createFrom(source: any = {}) {
	        return new PermissionAskRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.tool = source["tool"];
	        this.subject = source["subject"];
	        this.units = source["units"];
	        this.stage = source["stage"];
	        this.reason = source["reason"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class PendingInteraction {
	    kind: string;
	    permission?: PermissionAskRequest;
	    ask?: AskRequest;
	
	    static createFrom(source: any = {}) {
	        return new PendingInteraction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.permission = this.convertValues(source["permission"], PermissionAskRequest);
	        this.ask = this.convertValues(source["ask"], AskRequest);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RuleSource {
	    scope: string;
	    path: string;
	    exist: boolean;
	    mode?: string;
	    deny: number;
	    ask: number;
	    allow: number;
	
	    static createFrom(source: any = {}) {
	        return new RuleSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.path = source["path"];
	        this.exist = source["exist"];
	        this.mode = source["mode"];
	        this.deny = source["deny"];
	        this.ask = source["ask"];
	        this.allow = source["allow"];
	    }
	}
	export class RuleView {
	    rule: string;
	    bucket: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rule = source["rule"];
	        this.bucket = source["bucket"];
	        this.source = source["source"];
	    }
	}
	export class PermissionState {
	    sessionId: string;
	    mode: string;
	    projectDir: string;
	    rules: RuleView[];
	    sources: RuleSource[];
	    grants: Grant[];
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new PermissionState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.mode = source["mode"];
	        this.projectDir = source["projectDir"];
	        this.rules = this.convertValues(source["rules"], RuleView);
	        this.sources = this.convertValues(source["sources"], RuleSource);
	        this.grants = this.convertValues(source["grants"], Grant);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class SubagentInfo {
	    runId: string;
	    parentId: string;
	    title: string;
	    task: string;
	    status: string;
	    model?: string;
	    step: number;
	    currentTool?: string;
	    startedAt: number;
	    endedAt?: number;
	    summary?: string;
	    files?: string[];
	    declined?: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SubagentInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.parentId = source["parentId"];
	        this.title = source["title"];
	        this.task = source["task"];
	        this.status = source["status"];
	        this.model = source["model"];
	        this.step = source["step"];
	        this.currentTool = source["currentTool"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.summary = source["summary"];
	        this.files = source["files"];
	        this.declined = source["declined"];
	        this.error = source["error"];
	    }
	}
	export class Session {
	    id: string;
	    title: string;
	    project: string;
	    model: string;
	    permissionMode: string;
	    viewMode: string;
	    environment: string;
	    status: string;
	    startAt: number;
	    endAt: number;
	    messages: Message[];
	    conversations: Conversation[];
	    diffs?: DiffTurn[];
	    diffBaseline?: string;
	    diffTouched?: string[];
	    enabledTools?: string[];
	    enabledSkills?: string[];
	    contextSummary?: string;
	    contextCoveredUpTo?: number;
	    contextSummaryAt?: number;
	    parentId?: string;
	    subagent?: SubagentInfo;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.project = source["project"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.environment = source["environment"];
	        this.status = source["status"];
	        this.startAt = source["startAt"];
	        this.endAt = source["endAt"];
	        this.messages = this.convertValues(source["messages"], Message);
	        this.conversations = this.convertValues(source["conversations"], Conversation);
	        this.diffs = this.convertValues(source["diffs"], DiffTurn);
	        this.diffBaseline = source["diffBaseline"];
	        this.diffTouched = source["diffTouched"];
	        this.enabledTools = source["enabledTools"];
	        this.enabledSkills = source["enabledSkills"];
	        this.contextSummary = source["contextSummary"];
	        this.contextCoveredUpTo = source["contextCoveredUpTo"];
	        this.contextSummaryAt = source["contextSummaryAt"];
	        this.parentId = source["parentId"];
	        this.subagent = this.convertValues(source["subagent"], SubagentInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SessionConfig {
	    title: string;
	    project: string;
	    model: string;
	    permissionMode: string;
	    viewMode: string;
	    environment: string;
	    enabledTools: string[];
	    enabledSkills: string[];
	    parentId?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.project = source["project"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.environment = source["environment"];
	        this.enabledTools = source["enabledTools"];
	        this.enabledSkills = source["enabledSkills"];
	        this.parentId = source["parentId"];
	    }
	}
	export class SessionPatch {
	    title?: string;
	    model?: string;
	    permissionMode?: string;
	    viewMode?: string;
	    status?: string;
	    project?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionPatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.viewMode = source["viewMode"];
	        this.status = source["status"];
	        this.project = source["project"];
	    }
	}
	export class SkillFrontmatter {
	    name: string;
	    description: string;
	    version?: string;
	    license?: string;
	    author?: string;
	    allowedTools?: string[];
	    disableModelInvocation: boolean;
	    userInvocable: boolean;
	    metadata?: Record<string, any>;
	    extra?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new SkillFrontmatter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.license = source["license"];
	        this.author = source["author"];
	        this.allowedTools = source["allowedTools"];
	        this.disableModelInvocation = source["disableModelInvocation"];
	        this.userInvocable = source["userInvocable"];
	        this.metadata = source["metadata"];
	        this.extra = source["extra"];
	    }
	}
	export class SkillInstallInfo {
	    sourceType: string;
	    source?: string;
	    ref?: string;
	    subdir?: string;
	    installedAt?: number;
	    canUpdate: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SkillInstallInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceType = source["sourceType"];
	        this.source = source["source"];
	        this.ref = source["ref"];
	        this.subdir = source["subdir"];
	        this.installedAt = source["installedAt"];
	        this.canUpdate = source["canUpdate"];
	    }
	}
	export class SkillResource {
	    path: string;
	    size: number;
	    kind: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillResource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.kind = source["kind"];
	    }
	}
	export class SkillDetail {
	    id: string;
	    name: string;
	    description: string;
	    dir: string;
	    enabled: boolean;
	    builtin: boolean;
	    version?: string;
	    license?: string;
	    author?: string;
	    allowedTools?: string[];
	    disableModelInvocation: boolean;
	    userInvocable: boolean;
	    hasScripts: boolean;
	    resources?: SkillResource[];
	    errors?: string[];
	    warnings?: string[];
	    install?: SkillInstallInfo;
	    body: string;
	    frontmatter?: SkillFrontmatter;
	
	    static createFrom(source: any = {}) {
	        return new SkillDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.version = source["version"];
	        this.license = source["license"];
	        this.author = source["author"];
	        this.allowedTools = source["allowedTools"];
	        this.disableModelInvocation = source["disableModelInvocation"];
	        this.userInvocable = source["userInvocable"];
	        this.hasScripts = source["hasScripts"];
	        this.resources = this.convertValues(source["resources"], SkillResource);
	        this.errors = source["errors"];
	        this.warnings = source["warnings"];
	        this.install = this.convertValues(source["install"], SkillInstallInfo);
	        this.body = source["body"];
	        this.frontmatter = this.convertValues(source["frontmatter"], SkillFrontmatter);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SkillDraft {
	    name: string;
	    description: string;
	    version?: string;
	    license?: string;
	    author?: string;
	    allowedTools?: string[];
	    disableModelInvocation?: boolean;
	    userInvocable: boolean;
	    body: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillDraft(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.version = source["version"];
	        this.license = source["license"];
	        this.author = source["author"];
	        this.allowedTools = source["allowedTools"];
	        this.disableModelInvocation = source["disableModelInvocation"];
	        this.userInvocable = source["userInvocable"];
	        this.body = source["body"];
	    }
	}
	
	
	export class SkillInstallResult {
	    id: string;
	    name: string;
	    dir: string;
	    action: string;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new SkillInstallResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.dir = source["dir"];
	        this.action = source["action"];
	        this.warnings = source["warnings"];
	    }
	}
	export class SkillMeta {
	    id: string;
	    name: string;
	    description: string;
	    dir: string;
	    enabled: boolean;
	    builtin: boolean;
	    version?: string;
	    license?: string;
	    author?: string;
	    allowedTools?: string[];
	    disableModelInvocation: boolean;
	    userInvocable: boolean;
	    hasScripts: boolean;
	    resources?: SkillResource[];
	    errors?: string[];
	    warnings?: string[];
	    install?: SkillInstallInfo;
	
	    static createFrom(source: any = {}) {
	        return new SkillMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.dir = source["dir"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.version = source["version"];
	        this.license = source["license"];
	        this.author = source["author"];
	        this.allowedTools = source["allowedTools"];
	        this.disableModelInvocation = source["disableModelInvocation"];
	        this.userInvocable = source["userInvocable"];
	        this.hasScripts = source["hasScripts"];
	        this.resources = this.convertValues(source["resources"], SkillResource);
	        this.errors = source["errors"];
	        this.warnings = source["warnings"];
	        this.install = this.convertValues(source["install"], SkillInstallInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class ToolFileChange {
	    time: number;
	    tool: string;
	    path: string;
	    rel: string;
	    action: string;
	    added: number;
	    removed: number;
	    files?: DiffFile[];
	
	    static createFrom(source: any = {}) {
	        return new ToolFileChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.tool = source["tool"];
	        this.path = source["path"];
	        this.rel = source["rel"];
	        this.action = source["action"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.files = this.convertValues(source["files"], DiffFile);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ToolRuntimeStatus {
	    connected: boolean;
	    toolCount: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new ToolRuntimeStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.toolCount = source["toolCount"];
	        this.error = source["error"];
	    }
	}
	export class ToolParamConfig {
	    name: string;
	    description: string;
	    required: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolParamConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.required = source["required"];
	    }
	}
	export class ToolInfo {
	    id: string;
	    name: string;
	    label?: string;
	    description?: string;
	    kind: string;
	    icon?: string;
	    enabled: boolean;
	    builtin?: boolean;
	    exposure?: string;
	    parameters?: ToolParamConfig[];
	    cli?: CLIConfig;
	    http?: HTTPConfig;
	    mcp?: MCPConfig;
	    disabledTools?: string[];
	    discovered?: MCPToolMeta[];
	    status: ToolRuntimeStatus;
	
	    static createFrom(source: any = {}) {
	        return new ToolInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.icon = source["icon"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.exposure = source["exposure"];
	        this.parameters = this.convertValues(source["parameters"], ToolParamConfig);
	        this.cli = this.convertValues(source["cli"], CLIConfig);
	        this.http = this.convertValues(source["http"], HTTPConfig);
	        this.mcp = this.convertValues(source["mcp"], MCPConfig);
	        this.disabledTools = source["disabledTools"];
	        this.discovered = this.convertValues(source["discovered"], MCPToolMeta);
	        this.status = this.convertValues(source["status"], ToolRuntimeStatus);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class ToolSource {
	    id: string;
	    name: string;
	    label?: string;
	    description?: string;
	    kind: string;
	    icon?: string;
	    enabled: boolean;
	    builtin?: boolean;
	    exposure?: string;
	    parameters?: ToolParamConfig[];
	    cli?: CLIConfig;
	    http?: HTTPConfig;
	    mcp?: MCPConfig;
	    disabledTools?: string[];
	    discovered?: MCPToolMeta[];
	
	    static createFrom(source: any = {}) {
	        return new ToolSource(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.label = source["label"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.icon = source["icon"];
	        this.enabled = source["enabled"];
	        this.builtin = source["builtin"];
	        this.exposure = source["exposure"];
	        this.parameters = this.convertValues(source["parameters"], ToolParamConfig);
	        this.cli = this.convertValues(source["cli"], CLIConfig);
	        this.http = this.convertValues(source["http"], HTTPConfig);
	        this.mcp = this.convertValues(source["mcp"], MCPConfig);
	        this.disabledTools = source["disabledTools"];
	        this.discovered = this.convertValues(source["discovered"], MCPToolMeta);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UndoFileResult {
	    path: string;
	    action: string;
	    err?: string;
	
	    static createFrom(source: any = {}) {
	        return new UndoFileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.action = source["action"];
	        this.err = source["err"];
	    }
	}
	export class UndoResult {
	    turn: number;
	    files: UndoFileResult[];
	    failed: number;
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new UndoResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.turn = source["turn"];
	        this.files = this.convertValues(source["files"], UndoFileResult);
	        this.failed = source["failed"];
	        this.skipped = source["skipped"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

