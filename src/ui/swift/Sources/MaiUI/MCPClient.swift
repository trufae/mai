//
//  MCPClient.swift
//  MaiUI
//
//  Created by MaiUI on 2025-01-11.
//

import Foundation

struct JSONRPCRequest {
    let jsonrpc = "2.0"
    let id: Int
    let method: String
    let params: [String: Any]?

    func toJSONData() throws -> Data {
        print("JSONRPCRequest.toJSONData invoked for method: \(method), id: \(id)")
        var dict: [String: Any] = [
            "jsonrpc": jsonrpc,
            "id": id,
            "method": method
        ]
        if let params = params {
            dict["params"] = params
            print("JSONRPCRequest.toJSONData including params keys: \(Array(params.keys))")
        }
        let data = try JSONSerialization.data(withJSONObject: dict)
        print("JSONRPCRequest.toJSONData produced byte count: \(data.count)")
        return data
    }
}

struct JSONRPCNotification {
    let jsonrpc = "2.0"
    let method: String
    let params: [String: Any]?

    func toJSONData() throws -> Data {
        print("JSONRPCNotification.toJSONData invoked for method: \(method)")
        var dict: [String: Any] = [
            "jsonrpc": jsonrpc,
            "method": method
        ]
        if let params = params {
            dict["params"] = params
            print("JSONRPCNotification.toJSONData including params keys: \(Array(params.keys))")
        }
        let data = try JSONSerialization.data(withJSONObject: dict)
        print("JSONRPCNotification.toJSONData produced byte count: \(data.count)")
        return data
    }
}

struct JSONRPCResponse {
    let jsonrpc = "2.0"
    let id: Int?
    let result: [String: Any]?
    let error: RPCError?
}

struct RPCError: Codable {
    let code: Int
    let message: String
}


struct ToolCallResult {
    let content: Any?
    let isError: Bool
    let nextPageToken: String?
}

actor MCPClient {
    private var process: Process?
    private var stdin: Pipe?
    private var stdout: Pipe?
    private var stderr: Pipe?
    private var requestID = 1
    private var useHeaders = false

    deinit {
        print("MCPClient deinit triggered")
        // Clean up the process when the client is deallocated
        if let process = process, process.isRunning {
            print("MCPClient deinit terminating running process")
            process.terminate()
        }
    }

    private func findMaiExecutable() -> String {
        print("MCPClient.findMaiExecutable invoked")
        // Check if mai is in PATH
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        process.arguments = ["mai"]

        let pipe = Pipe()
        process.standardOutput = pipe

        do {
            try process.run()
            process.waitUntilExit()

            if process.terminationStatus == 0 {
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                if let path = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
                   !path.isEmpty {
                    print("MCPClient.findMaiExecutable found path via which: \(path)")
                    return path
                }
            } else {
                print("MCPClient.findMaiExecutable which terminated with status: \(process.terminationStatus)")
            }
        } catch {
            print("MCPClient.findMaiExecutable which lookup failed: \(error)")
        }

        // Fallback locations
        let commonPaths = [
            "/usr/local/bin/mai",
            "/opt/homebrew/bin/mai",
            "/usr/bin/mai"
        ]

        for path in commonPaths {
            if FileManager.default.fileExists(atPath: path) {
                print("MCPClient.findMaiExecutable found path in common paths: \(path)")
                return path
            }
        }

        // Last resort - assume it's in PATH
        print("MCPClient.findMaiExecutable falling back to PATH")
        return "mai"
    }

    func initialize() async throws {
        print("MCPClient.initialize invoked")
        try await spawnProcess()

        let request = JSONRPCRequest(id: requestID, method: "initialize", params: [
            "protocolVersion": "2024-11-05",
            "capabilities": [:],
            "clientInfo": [
                "name": "MaiUI",
                "version": "1.0.0"
            ]
        ])
        requestID += 1
        print("MCPClient.initialize sending initialize request with id: \(request.id)")

        let response = try await sendRequest(request)
        if let error = response.error {
            print("MCPClient.initialize received error response: \(error)")
            throw NSError(domain: "MCP", code: error.code, userInfo: [NSLocalizedDescriptionKey: error.message])
        }
        print("MCPClient.initialize received success response")

        // Send initialized notification
        let notification = JSONRPCNotification(method: "notifications/initialized", params: nil)
        print("MCPClient.initialize sending initialized notification")
        try sendNotification(notification)
    }

    func callTool(_ name: String, arguments: [String: Any]) async throws -> String {
        print("MCPClient.callTool invoked for tool: \(name) with arguments: \(arguments)")
        let params: [String: Any] = [
            "name": name,
            "arguments": arguments
        ]

        let request = JSONRPCRequest(id: requestID, method: "tools/call", params: params)
        requestID += 1
        print("MCPClient.callTool sending request id: \(request.id)")

        let response = try await sendRequest(request)
        if let error = response.error {
            print("MCPClient.callTool received error: \(error)")
            throw NSError(domain: "MCP", code: error.code, userInfo: [NSLocalizedDescriptionKey: error.message])
        }

        guard let result = response.result else {
            print("MCPClient.callTool missing result in response")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No result in response"])
        }

        if let isError = result["isError"] as? Bool, isError {
            print("MCPClient.callTool result flagged as error")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "Tool call failed"])
        }

        if let content = result["content"] as? [[String: Any]],
           let firstContent = content.first,
           let text = firstContent["text"] as? String {
            print("MCPClient.callTool returning text content")
            return text
        }

        print("MCPClient.callTool returning default response message")
        return "Response received"
    }

    private func spawnProcess() async throws {
        print("MCPClient.spawnProcess invoked")
        process = Process()

        // Try to find mai in PATH first, fallback to common locations
        let maiPath = findMaiExecutable()
        print("MCPClient.spawnProcess resolved executable path: \(maiPath)")

        // Check if mai exists before trying to run it
        if !FileManager.default.fileExists(atPath: maiPath) {
            print("MCPClient.spawnProcess could not find MAI executable at path")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "MAI executable not found at \(maiPath). Please make sure MAI is installed and in your PATH."])
        }

        process?.executableURL = URL(fileURLWithPath: maiPath)
        process?.arguments = ["-M"]

        stdin = Pipe()
        stdout = Pipe()
        stderr = Pipe()

        process?.standardInput = stdin
        process?.standardOutput = stdout
        process?.standardError = stderr

        print("MCPClient.spawnProcess launching process")
        try process?.run()

        // Give the process a moment to initialize
        try await Task.sleep(nanoseconds: 500_000_000) // 0.5 seconds
        print("MCPClient.spawnProcess completed initial delay")

        // Check if the process is still running
        if let process = process, process.isRunning == false {
            let terminationStatus = process.terminationStatus
            print("MCPClient.spawnProcess detected early termination with status: \(terminationStatus)")
            throw NSError(domain: "MCP", code: Int(terminationStatus), userInfo: [NSLocalizedDescriptionKey: "MAI process exited early with status \(terminationStatus)"])
        }
        print("MCPClient.spawnProcess confirmed process is running")
    }

    private func sendRequest(_ request: JSONRPCRequest) async throws -> JSONRPCResponse {
        print("MCPClient.sendRequest invoked with id: \(request.id) and method: \(request.method)")
        guard let stdin = stdin else {
            print("MCPClient.sendRequest missing stdin pipe")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No stdin pipe"])
        }

        let data = try request.toJSONData()
        var message = String(data: data, encoding: .utf8) ?? ""

        if useHeaders {
            let header = "Content-Length: \(data.count)\r\n\r\n"
            message = header + message
        } else {
            message += "\n"
        }

        guard let messageData = message.data(using: .utf8) else {
            print("MCPClient.sendRequest failed to encode message as data")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "Failed to encode message"])
        }

        try stdin.fileHandleForWriting.write(contentsOf: messageData)
        print("MCPClient.sendRequest wrote message bytes: \(messageData.count)")

        print("MCPClient.sendRequest awaiting response")
        return try await self.readResponse()
    }

    private func sendNotification(_ notification: JSONRPCNotification) throws {
        print("MCPClient.sendNotification invoked for method: \(notification.method)")
        guard let stdin = stdin else {
            print("MCPClient.sendNotification missing stdin pipe")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No stdin pipe"])
        }

        let data = try notification.toJSONData()
        var message = String(data: data, encoding: .utf8) ?? ""

        if useHeaders {
            let header = "Content-Length: \(data.count)\r\n\r\n"
            message = header + message
        } else {
            message += "\n"
        }

        guard let messageData = message.data(using: .utf8) else {
            print("MCPClient.sendNotification failed to encode message as data")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "Failed to encode notification"])
        }

        try stdin.fileHandleForWriting.write(contentsOf: messageData)
        print("MCPClient.sendNotification wrote message bytes: \(messageData.count)")
    }



    private func readResponse() async throws -> JSONRPCResponse {
        print("MCPClient.readResponse invoked")
        guard let stdout = stdout else {
            print("MCPClient.readResponse missing stdout pipe")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No stdout pipe"])
        }

        // Check if process is still running
        if let process = process, !process.isRunning {
            print("MCPClient.readResponse detected process exit")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "MAI process has exited"])
        }

        let fileHandle = stdout.fileHandleForReading

        print("MCPClient.readResponse waiting for data")
        // Read response, handling both header-based and line-delimited formats
        var data: Data

        var firstLine = try fileHandle.readLine()
        print("MCPClient.readResponse first line: \(firstLine)")
        var emptyLineCount = 0
        while firstLine.isEmpty {
            emptyLineCount += 1
            print("MCPClient.readResponse encountered empty line number: \(emptyLineCount)")
            guard emptyLineCount <= 3 else {
                print("MCPClient.readResponse exceeded empty line threshold")
                throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No response from MAI"])
            }
            firstLine = try fileHandle.readLine()
        }

        if let firstCharacter = firstLine.first, firstCharacter == "{" || firstCharacter == "[" {
            print("MCPClient.readResponse detected JSON without headers")
            data = Data(firstLine.utf8)
        } else {
            print("MCPClient.readResponse processing headers starting with line: \(firstLine)")
            var contentLength: Int?
            var headerLine = firstLine

            while true {
                let lowercased = headerLine.lowercased()
                if lowercased.hasPrefix("content-length:") {
                    let parts = headerLine.split(separator: ":", maxSplits: 1)
                    if parts.count == 2 {
                        contentLength = Int(parts[1].trimmingCharacters(in: .whitespaces))
                        print("MCPClient.readResponse parsed Content-Length: \(String(describing: contentLength))")
                    }
                }

                let nextLine = try fileHandle.readLine()
                if nextLine.isEmpty {
                    print("MCPClient.readResponse reached end of headers")
                    break
                }
                headerLine = nextLine
                print("MCPClient.readResponse read header line: \(headerLine)")
            }

            if let length = contentLength {
                self.useHeaders = true
                print("MCPClient.readResponse reading body with length: \(length)")
                var body = Data()
                var remaining = length
                while remaining > 0 {
                    guard let chunk = try fileHandle.read(upToCount: remaining), !chunk.isEmpty else {
                        print("MCPClient.readResponse received incomplete chunk")
                        throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "Incomplete response from MAI"])
                    }
                    body.append(chunk)
                    remaining -= chunk.count
                    print("MCPClient.readResponse read chunk of size: \(chunk.count), remaining: \(remaining)")
                }
                data = body
            } else {
                print("MCPClient.readResponse reading fallback body data")
                guard let bodyData = try fileHandle.read(upToCount: 1024), !bodyData.isEmpty else {
                    print("MCPClient.readResponse fallback body empty")
                    throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "No response from MAI"])
                }
                data = bodyData
            }
        }

        // Parse with JSONSerialization
        guard let jsonObject = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            print("MCPClient.readResponse failed to decode JSON")
            throw NSError(domain: "MCP", code: -1, userInfo: [NSLocalizedDescriptionKey: "Invalid JSON response"])
        }

        let id = jsonObject["id"] as? Int
        let result = jsonObject["result"] as? [String: Any]
        let errorDict = jsonObject["error"] as? [String: Any]
        var error: RPCError?
        if let errorDict = errorDict {
            error = RPCError(
                code: errorDict["code"] as? Int ?? 0,
                message: errorDict["message"] as? String ?? ""
            )
            print("MCPClient.readResponse parsed error: \(String(describing: error))")
        }

        print("MCPClient.readResponse returning response with id: \(String(describing: id))")
        return JSONRPCResponse(id: id, result: result, error: error)
    }
}

extension FileHandle {
    func readLine() throws -> String {
        var data = Data()
        while true {
            guard let chunk = try read(upToCount: 1) else {
                print("FileHandle.readLine reached end of file")
                break
            }
            if chunk.isEmpty {
                print("FileHandle.readLine encountered empty chunk")
                break
            }
            if let byte = chunk.first, byte == 10 { // \n
                break
            }
            data.append(chunk)
        }
        let line = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        print("FileHandle.readLine returning line: \(line)")
        return line
    }
}
