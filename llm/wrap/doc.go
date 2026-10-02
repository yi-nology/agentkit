// Package wrap eino BaseChatModel 装饰器集：窗口拟合（FitModel）、输出截断续写
// （ContinueModel）、限速（RateModel）。三者职责正交、可任意组合（推荐外层顺序
// Rate→Fit→Continue：限速最内层保物理调用同桶，续写最外层对最终输出负责），
// WithTools 均做绑定透传再包装——ReAct 绑定工具后装饰不失效。
//
// 与 llm.Client 的分工：Client.fitInput 服务 Generator 链（单级截最长 user，
// 内嵌拟合）；本包装饰器服务直调旁路（ReAct/RawModel 等裸 BaseChatModel），
// 三级递进且带真实 usage 校准。同进程两路并存无副作用（拟合只紧不松、幂等）。
//
// 收编自 argus internal/context（v0.10.32）：ctxfit/ctxcontinue/ctxrate 三装饰器
// 在 PR 审查Bot 生产验证 ~1 个月（v3.36.0–v3.75），归位为 SDK 通用能力；观测
// 依赖已回调化（OnFit/OnContinue/OnBreakdown 均可 nil）。
package wrap
