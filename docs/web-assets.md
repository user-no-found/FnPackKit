# 静态资源传输

`runtime/webassets` 为应用明确声明的内置脚本、样式、字体和图片提供 gzip 与浏览器私有缓存。页面、接口、用户文件、错误响应和设置 Cookie 的响应保持 `no-store`。资源使用 `private, no-cache` 和 ETag，每次复用仍向服务端校验，因此授权和应用暂停检查必须在资源处理之前执行。

代理接入由应用识别自己的内置资源目录，在 `ReverseProxy.Rewrite` 调用：

```go
webassets.Negotiate(request.Out, request.In, isBundledAsset(request.In.URL.Path))
```

在响应重定向、Cookie 过滤和 `runtimepanel.Inject` 之前调用：

```go
if err := webassets.Response(response, isBundledAsset(publicPath)); err != nil {
    return err
}
```

`publicPath` 由应用按代理的前缀重写规则还原。外层处理器不能额外追加 `Cache-Control: no-store` 到已验证的静态响应；错误和暂停响应继续使用各自的 `no-store`。该包不替代来源策略、NAS 身份校验、访问权限或后端暂停门。

只有声明的静态请求允许向上游协商 gzip。HTML 继续使用 identity，静态路径意外返回的 gzip HTML 会在注入前有界解压。非静态响应、WebSocket 和事件流不会缓冲或压缩。静态正文缓冲上限为 8 MiB，更大资源保持原有传输；已有 gzip、字体和图片的上游验证器保持有效。无需进程级正文缓存。

直接从只读发布目录提供资源时，可以在现有授权处理器内使用：

```go
assets := webassets.FileServer(http.Dir(publicDirectory))
http.StripPrefix(applicationPrefix, assets).ServeHTTP(writer, request)
```

调用方仍须限制路由到发布资源，禁止把用户存储目录交给该处理器。HEAD、条件请求和范围请求保留 HTTP 语义；用户数据和 API 不应使用这个接口。
