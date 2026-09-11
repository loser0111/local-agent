export namespace main {
	
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
	    }
	}
	export class ChatResult {
	    reply: string;
	    toolCalls?: ToolCall[];
	    messages?: Message[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reply = source["reply"];
	        this.toolCalls = this.convertValues(source["toolCalls"], ToolCall);
	        this.messages = this.convertValues(source["messages"], Message);
	        this.error = source["error"];
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
	
	export class Model {
	    name: string;
	    alias: string;
	    modelId: string;
	    apiKey: string;
	    url: string;
	
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
	    }
	}
	export class Session {
	    id: string;
	    title: string;
	    project: string;
	    model: string;
	    permissionMode: string;
	    environment: string;
	    status: string;
	    startAt: number;
	    endAt: number;
	    messages: Message[];
	    conversations: Conversation[];
	
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
	        this.environment = source["environment"];
	        this.status = source["status"];
	        this.startAt = source["startAt"];
	        this.endAt = source["endAt"];
	        this.messages = this.convertValues(source["messages"], Message);
	        this.conversations = this.convertValues(source["conversations"], Conversation);
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
	    environment: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.project = source["project"];
	        this.model = source["model"];
	        this.permissionMode = source["permissionMode"];
	        this.environment = source["environment"];
	    }
	}
	export class SessionPatch {
	    title?: string;
	    model?: string;
	    permissionMode?: string;
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
	        this.status = source["status"];
	        this.project = source["project"];
	    }
	}

}

