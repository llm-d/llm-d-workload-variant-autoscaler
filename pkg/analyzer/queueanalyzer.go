package analyzer

import (
	"fmt"
)

// small disturbance around a value
const Epsilon = float32(0.001)

// fraction of maximum server throughput to provide stability (running this fraction below the maximum)
const StabilitySafetyFraction = float32(0.1)

// Analyzer of inference server queue
type QueueAnalyzer struct {
	MaxBatchSize int                     // maximum batch size
	MaxQueueSize int                     // maximum queue size
	ServiceParms *ServiceParms           // request processing parameters
	RequestSize  *RequestSize            // number of input and output tokens per request
	Model        *MM1ModelStateDependent // queueing model
	RateRange    *RateRange              // range of request rates for model stability
}

// queue configuration parameters
type Configuration struct {
	MaxBatchSize int           // maximum batch size (limit on the number of requests concurrently receiving service >0)
	MaxQueueSize int           // maximum queue size (limit on the number of requests queued for servive >=0)
	ServiceParms *ServiceParms // request processing parameters
}

// request processing parameters
type ServiceParms struct {
	Prefill *PrefillParms // parameters to calculate prefill time
	Decode  *DecodeParms  // parameters to calculate decode time
}

// prefill time = gamma + delta * inputTokens * batchSize (msec); inputTokens > 0
type PrefillParms struct {
	Gamma float32 // base
	Delta float32 // slope
}

// decode time = alpha + beta * batchSize (msec); batchSize > 0
type DecodeParms struct {
	Alpha float32 // base
	Beta  float32 // slope
}

// request tokens data
type RequestSize struct {
	AvgInputTokens  int // average number of input tokens per request
	AvgOutputTokens int // average number of output tokens per request
}

// range of request rates (requests/sec)
type RateRange struct {
	Min float32 // lowest rate (slightly larger than zero)
	Max float32 // highest rate (slightly less than maximum service rate)
}

// analysis solution metrics data
type AnalysisMetrics struct {
	Throughput     float32 // effective throughput (requests/sec)
	AvgRespTime    float32 // average request response time (aka latency) (msec)
	AvgWaitTime    float32 // average request queueing time (msec)
	AvgNumInServ   float32 // average number of requests in service
	AvgPrefillTime float32 // average request prefill time (msec)
	AvgTokenTime   float32 // average token decode time (msec)
	MaxRate        float32 // maximum throughput (requests/sec)
	Rho            float32 // utilization
}

// queue performance targets
type TargetPerf struct {
	TargetTTFT float32 // target time to first token (queueing + prefill) (msec)
	TargetITL  float32 // target inter-token latency (msec)
	TargetTPS  float32 // target token generation throughtput (tokens/sec)
}

// queue max request rates to achieve performance targets
type TargetRate struct {
	RateTargetTTFT float32 // max request rate for target TTFT (requests/sec)
	RateTargetITL  float32 // max request rate for target ITL (requests/sec)
	RateTargetTPS  float32 // max request rate for target TPS (requests/sec)
}

// create a new queue analyzer from config
func NewQueueAnalyzer(qConfig *Configuration, requestSize *RequestSize) (*QueueAnalyzer, error) {
	if err := qConfig.check(); err != nil {
		return nil, err
	}
	if err := requestSize.check(); err != nil {
		return nil, err
	}
	// build queueing model
	return BuildModel(qConfig, requestSize), nil
}

// build queueing model using service rates, leaving arrival rate as parameter
func BuildModel(qConfig *Configuration, requestSize *RequestSize) (modelData *QueueAnalyzer) {
	parms := qConfig.ServiceParms

	// calculate state-dependent service rate
	servRate := make([]float32, qConfig.MaxBatchSize)
	for n := 1; n <= qConfig.MaxBatchSize; n++ {
		prefillTime := parms.Prefill.PrefillTime(requestSize.AvgInputTokens, float32(n))
		numDecode := requestSize.AvgOutputTokens - 1 // number of decodes (one per output token except the first)
		// special case: allow one decode in case of decode only and one output token
		if requestSize.AvgInputTokens == 0 && requestSize.AvgOutputTokens == 1 {
			numDecode = 1
		}
		decodeTime := float32(numDecode) * parms.Decode.DecodeTime(float32(n))
		servRate[n-1] = float32(n) / (prefillTime + decodeTime)
	}

	// set and check limits
	lambdaMin := servRate[0] * Epsilon
	lambdaMax := servRate[qConfig.MaxBatchSize-1] * (1 - Epsilon)
	rateRange := &RateRange{Min: lambdaMin * 1000, Max: lambdaMax * 1000}

	// create and solve model
	occupancyUpperBound := qConfig.MaxQueueSize + qConfig.MaxBatchSize
	model := NewMM1ModelStateDependent(occupancyUpperBound, servRate)
	return &QueueAnalyzer{
		MaxBatchSize: qConfig.MaxBatchSize,
		MaxQueueSize: qConfig.MaxQueueSize,
		ServiceParms: parms,
		RequestSize:  requestSize,
		Model:        model,
		RateRange:    rateRange,
	}
}

// evaluate performance metrics given request rate
func (qa *QueueAnalyzer) Analyze(requestRate float32) (metrics *AnalysisMetrics, err error) {
	if requestRate <= 0 {
		return nil, fmt.Errorf("invalid request rate %v", requestRate)
	}
	model := qa.Model
	rateRange := qa.RateRange
	if requestRate > rateRange.Max {
		err = fmt.Errorf("rate=%v, max allowed rate=%v", requestRate, rateRange.Max)
		return nil, err
	}

	//solve model
	model.Solve(requestRate/1000, 1)
	if !model.IsValid() {
		err = fmt.Errorf("invalid model %s", model)
		return nil, err
	}

	// get statistics
	avgNumInServ := model.GetAvgNumInServers()

	effConc := EffectiveConcurrency(model.GetAvgServTime(), qa.ServiceParms, qa.RequestSize, qa.MaxBatchSize)
	prefillTime := qa.ServiceParms.Prefill.PrefillTime(qa.RequestSize.AvgInputTokens, effConc)
	tokenTime := qa.ServiceParms.Decode.DecodeTime(effConc)

	rho := avgNumInServ / float32(qa.MaxBatchSize)
	rho = min(max(rho, 0), 1)

	// return solution
	metrics = &AnalysisMetrics{
		Throughput:     model.GetThroughput() * 1000,
		AvgRespTime:    model.GetAvgRespTime(),
		AvgWaitTime:    model.GetAvgWaitTime(),
		AvgNumInServ:   avgNumInServ,
		AvgPrefillTime: prefillTime,
		AvgTokenTime:   tokenTime,
		MaxRate:        rateRange.Max,
		Rho:            rho,
	}
	return metrics, nil
}

// global variables used by eval functions, to be set before calling eval function
var evalRequestSize *RequestSize   // number of input and output tokens per request
var evalServiceParms *ServiceParms // request processing parameters for prefill and decode stages
var evalMaxBatchSize int           // max batch size

// evaluate max request rates to achieve a given target performance, returns
//   - max request rates
//   - performance metrics at min of max request rates
//   - achieved values of targets
func (qa *QueueAnalyzer) Size(targetPerf *TargetPerf) (targetRate *TargetRate, metrics *AnalysisMetrics, achieved *TargetPerf, err error) {
	if err := targetPerf.check(); err != nil {
		return nil, nil, nil, err
	}
	targetTTFT := targetPerf.TargetTTFT
	targetITL := targetPerf.TargetITL
	targetTPS := targetPerf.TargetTPS

	lambdaMin := qa.RateRange.Min / 1000
	lambdaMax := qa.RateRange.Max / 1000

	// set global variables for model and parameters used in functional evaluation
	Model = qa.Model
	evalRequestSize = qa.RequestSize
	evalServiceParms = qa.ServiceParms
	evalMaxBatchSize = qa.MaxBatchSize

	var ind int

	// find max rate to achieve target TTFT time
	lambdaStarTTFT := lambdaMax
	if targetTTFT > 0 {
		lambdaStarTTFT, ind, err = BinarySearch(lambdaMin, lambdaMax, targetTTFT, EvalTTFT)
		if ind < 0 {
			err = fmt.Errorf("target is below the bounded region")
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to calculate lambdaStarTTFT, targetTTFT=%v, range=%s, ind=%d, err=%v",
				targetTTFT, qa.RateRange, ind, err)
		}
	}

	// find max rate to achieve target ITL time
	lambdaStarITL := lambdaMax
	if targetITL > 0 {
		lambdaStarITL, ind, err = BinarySearch(lambdaMin, lambdaMax, targetITL, EvalITL)
		if ind < 0 {
			err = fmt.Errorf("target is below the bounded region")
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to calculate lambdaStarITL, targetITL=%v, range=%s, ind=%d, err=%v",
				targetITL, qa.RateRange, ind, err)
		}
	}

	// find max rate to achieve target TPS
	lambdaStarTPS := lambdaMax
	if targetTPS > 0 {
		lambdaStarTPS = lambdaMax * (1 - StabilitySafetyFraction)
	}

	// analyze queue with smaller of rates
	lambda := min(lambdaStarTTFT, lambdaStarITL, lambdaStarTPS)
	requestRate := lambda * 1000 // convert to per-second rate
	if metrics, err = qa.Analyze(requestRate); err != nil {
		return nil, nil, nil, err
	}

	targetRate = &TargetRate{
		RateTargetTTFT: lambdaStarTTFT * 1000,
		RateTargetITL:  lambdaStarITL * 1000,
		RateTargetTPS:  lambdaStarTPS * 1000,
	}

	achieved = &TargetPerf{
		TargetTTFT: metrics.AvgWaitTime + metrics.AvgPrefillTime,
		TargetITL:  metrics.AvgTokenTime,
		TargetTPS:  metrics.Throughput * float32(qa.RequestSize.AvgOutputTokens),
	}
	return targetRate, metrics, achieved, nil
}

func (p *PrefillParms) PrefillTime(avgInputTokens int, batchSize float32) float32 {
	if avgInputTokens == 0 {
		return 0
	}
	return p.Gamma + p.Delta*float32(avgInputTokens)*batchSize
}

func (p *DecodeParms) DecodeTime(batchSize float32) float32 {
	return p.Alpha + p.Beta*batchSize
}

// Function used in binary search (target TTFT)
//   - x is lambda req/msec
func EvalTTFT(x float32) (float32, error) {
	Model.Solve(x, 1)
	if !Model.IsValid() {
		return 0, fmt.Errorf("invalid model %s", Model)
	}
	avgWaitTime := Model.GetAvgWaitTime()
	effConc := EffectiveConcurrency(Model.GetAvgServTime(), evalServiceParms, evalRequestSize, evalMaxBatchSize)
	ttft := avgWaitTime + evalServiceParms.Prefill.PrefillTime(evalRequestSize.AvgInputTokens, effConc)
	return ttft, nil
}

// Function used in binary search (target ITL)
//   - x is lambda req/msec
func EvalITL(x float32) (float32, error) {
	Model.Solve(x, 1)
	if !Model.IsValid() {
		return 0, fmt.Errorf("invalid model %s", Model)
	}
	effConc := EffectiveConcurrency(Model.GetAvgServTime(), evalServiceParms, evalRequestSize, evalMaxBatchSize)
	return evalServiceParms.Decode.DecodeTime(effConc), nil
}

// calculate effective average number of requests in service (n), given average request service time
//   - n has to satisfy: prefillTime(n) + totalDecodeTime(n) = avgServiceTime
//   - prefillTime(n) = gamma + delta * inTokens * n
//   - totalDecodeTime(n) = (alpha + beta * n) * (outTokens - 1)
func EffectiveConcurrency(avgServiceTime float32, serviceParms *ServiceParms, requestSize *RequestSize, maxBatchSize int) float32 {
	tokens := float32(requestSize.AvgOutputTokens - 1)
	numerator := avgServiceTime - (serviceParms.Prefill.Gamma + serviceParms.Decode.Alpha*tokens)
	denominator := (serviceParms.Prefill.Delta * float32(requestSize.AvgInputTokens)) + (serviceParms.Decode.Beta * tokens)
	n := numerator / denominator
	return min(max(n, 0), float32(maxBatchSize))
}

// check validity of configuration parameters
func (c *Configuration) check() error {
	if c.MaxBatchSize <= 0 || c.MaxQueueSize < 0 || c.ServiceParms == nil ||
		c.ServiceParms.Prefill == nil || c.ServiceParms.Decode == nil ||
		c.ServiceParms.Decode.Alpha <= 0 || c.ServiceParms.Decode.Beta < 0 ||
		c.ServiceParms.Prefill.Gamma <= 0 || c.ServiceParms.Prefill.Delta < 0 {
		return fmt.Errorf("invalid configuration %s", c)
	}
	return nil
}

// check validity of request size
func (rq *RequestSize) check() error {
	if rq.AvgInputTokens < 0 || rq.AvgOutputTokens < 1 {
		return fmt.Errorf("invalid request size %s", rq)
	}
	return nil
}

// check validity of target values
func (targetPerf *TargetPerf) check() error {
	if targetPerf.TargetITL < 0 ||
		targetPerf.TargetTTFT < 0 ||
		targetPerf.TargetTPS < 0 {
		return fmt.Errorf("invalid target data values %s", targetPerf)
	}
	return nil
}

/*
 * toString() functions
 */

func (c *Configuration) String() string {
	return fmt.Sprintf("{maxBatch=%d, maxQueue=%d, servParms:%s}",
		c.MaxBatchSize, c.MaxQueueSize, c.ServiceParms)
}

func (qa *QueueAnalyzer) String() string {
	return fmt.Sprintf("{maxBatch=%d, maxQueue=%d, servParms:%s, reqSize:%s, model:%s, rates:%s}",
		qa.MaxBatchSize, qa.MaxQueueSize, qa.ServiceParms, qa.RequestSize, qa.Model, qa.RateRange)
}

func (sp *ServiceParms) String() string {
	return fmt.Sprintf("{prefillParms=%s, decodeParms=%s}",
		sp.Prefill, sp.Decode)
}

func (p *PrefillParms) String() string {
	return fmt.Sprintf("{gamma=%.3f, delta=%.5f}", p.Gamma, p.Delta)
}

func (p *DecodeParms) String() string {
	return fmt.Sprintf("{alpha=%.3f, beta=%.5f}", p.Alpha, p.Beta)
}

func (rq *RequestSize) String() string {
	return fmt.Sprintf("{inTokens=%d, outTokens=%d}", rq.AvgInputTokens, rq.AvgOutputTokens)
}

func (rr *RateRange) String() string {
	return fmt.Sprintf("[%.3f, %.3f]", rr.Min, rr.Max)
}

func (am *AnalysisMetrics) String() string {
	return fmt.Sprintf("{tput=%.3f, lat=%.3f, wait=%.3f, conc=%.3f, prefill=%.3f, itl=%.3f, maxRate=%.3f, rho=%0.3f}",
		am.Throughput, am.AvgRespTime, am.AvgWaitTime, am.AvgNumInServ, am.AvgPrefillTime, am.AvgTokenTime, am.MaxRate, am.Rho)
}

func (tp *TargetPerf) String() string {
	return fmt.Sprintf("{TTFT=%.3f, ITL=%.3f, TPS=%.3f}",
		tp.TargetTTFT, tp.TargetITL, tp.TargetTPS)
}

func (tr *TargetRate) String() string {
	return fmt.Sprintf("{rateTTFT=%.3f, rateITL=%.3f, rateTPS=%.3f}",
		tr.RateTargetTTFT, tr.RateTargetITL, tr.RateTargetTPS)
}
