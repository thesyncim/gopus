.PHONY: lint lint-fix test test-fast test-race test-fuzz-smoke test-fuzz-safety test-consumer-smoke test-examples-smoke test-doc-contract test-dnn-blob-parity test-dred-tag test-qext-parity test-unsupported-controls-tag test-unsupported-controls-parity test-unsupported-controls-parity-experimental test-quality test-exactness quality-report test-exhaustive test-provenance test-assembly-safety test-soak-safety bench-guard bench-libopus-guard bench-decoder-libopus-guard bench-encoder-libopus-guard bench-testvectors bench-testvectors-compare bench-testvectors-report verify-production verify-production-exhaustive verify-safety release-evidence release-preflight ensure-libopus ensure-libopus-qext ensure-testvectors fixtures-gen fixtures-gen-decoder fixtures-gen-decoder-loss fixtures-gen-encoder fixtures-gen-variants fixtures-gen-amd64 docker-buildx-bootstrap docker-build docker-build-exhaustive docker-test docker-test-exhaustive docker-shell build build-nopgo pgo-generate pgo-build clean clean-vectors bench-kernels

GO ?= go
GO_WORK_ENV ?= GOWORK=off
GOLANGCI_LINT ?= golangci-lint
GOLANGCI_LINT_VERSION ?= v1.64.8
GO_RUNNABLE_TEST ?= bash ./tools/run_go_test_runnable.sh
ASSEMBLY_SAFETY_MATRIX ?= bash ./tools/run_assembly_safety_matrix.sh
PGO_FILE ?= default.pgo
PGO_FLAG ?= -pgo=$(PGO_FILE)
PGO_GENERATE_FLAG ?= -pgo=off
PGO_REPORT_PROFILE ?= $(PGO_FILE)
PGO_BENCH ?= ^Benchmark(DecoderDecode_CELT|DecoderDecodeInt16|DecoderDecode_Stereo|EncoderEncode_CallerBuffer|EncoderEncodeInt16|EncoderEncode_Restricted(CELT|CELT5ms|SILK)CBRStreamAfterReset)$$
PGO_PKG ?= .
PGO_BENCHTIME ?= 20s
PGO_COUNT ?= 1
LIBOPUS_VERSION ?= 1.6.1
DOCKER_IMAGE ?= gopus-ci
DOCKERFILE_CI ?= Dockerfile.ci
DOCKER_DISABLE_OPUSDEC ?= 0
DOCKER_DISABLE_OPUSENC ?= 1
DOCKER_CACHE_DIR ?= .docker-cache
DOCKER_BUILDER ?= gopus-buildx
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_M),arm64)
DOCKER_PLATFORM ?= linux/arm64
else ifeq ($(UNAME_M),aarch64)
DOCKER_PLATFORM ?= linux/arm64
else
DOCKER_PLATFORM ?= linux/amd64
endif
DOCKER_CACHE_SUFFIX := $(subst /,-,$(DOCKER_PLATFORM))
DOCKER_EXHAUSTIVE_PLATFORM ?= linux/amd64
DOCKER_EXHAUSTIVE_CACHE_SUFFIX := $(subst /,-,$(DOCKER_EXHAUSTIVE_PLATFORM))
DOCKER_BUILDX_CACHE_DIR := $(DOCKER_CACHE_DIR)/buildx-$(DOCKER_CACHE_SUFFIX)
DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR := $(DOCKER_CACHE_DIR)/buildx-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX)
RELEASE_EVIDENCE_DIR ?= reports/release
QUALITY_REPORT_DIR ?= reports/quality
GOPUS_SAFETY_FUZZTIME ?= 12s
GOPUS_SAFETY_PARSER_FUZZTIME ?= $(GOPUS_SAFETY_FUZZTIME)
GOPUS_FUZZ_SMOKE_FUZZTIME ?= 50000x
GOPUS_SAFETY_SOAK_DURATION ?= 30s
GOPUS_SAFETY_SOAK_REPORT_INTERVAL ?= 10s
GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB ?= 256
GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH ?= 16
GOPUS_SAFETY_SOAK_MAX_ALLOCS ?= 0.0
BENCH_TESTVECTORS_COMPARE_TIME ?= 200ms
BENCH_TESTVECTORS_COMPARE_TIMES ?=
BENCH_TESTVECTORS_COMPARE_COUNT ?= 3
BENCH_TESTVECTORS_COMPARE_CASES ?= all
BENCH_TESTVECTORS_COMPARE_PATHS ?= all
BENCH_TESTVECTORS_COMPARE_TIME_FLAG = $(if $(BENCH_TESTVECTORS_COMPARE_TIMES),-benchtimes=$(BENCH_TESTVECTORS_COMPARE_TIMES),-benchtime=$(BENCH_TESTVECTORS_COMPARE_TIME))
BENCH_LIBOPUS_GUARD_TIME ?= 200ms
BENCH_LIBOPUS_GUARD_COUNT ?= 3
BENCH_LIBOPUS_GUARD_RATIO ?= 1.60
BENCH_LIBOPUS_GUARD_ALLOCS ?= 0
BENCH_ENCODER_LIBOPUS_GUARD_RATIO ?= 2.50
BENCH_ENCODER_LIBOPUS_GUARD_ALLOCS ?= 0
BENCH_ENCODER_LIBOPUS_GUARD_CASES ?= all
TEST_VECTOR_URL ?= https://opus-codec.org/static/testvectors/opus_testvectors-rfc8251.tar.gz
TEST_VECTOR_FALLBACK_URL ?= https://www.ietf.org/proceedings/98/slides/materials-98-codec-opus-newvectors-00.tar.gz
GO_TEST_FAST = GOPUS_TEST_TIER=fast $(GO_WORK_ENV) $(GO) test
GO_TEST_PARITY = GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test
GO_TEST_PARITY_EXACT = GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 GOPUS_LIBOPUS_EXACTNESS=1 $(GO_WORK_ENV) $(GO) test
GO_TEST_EXHAUSTIVE = GOPUS_TEST_TIER=exhaustive $(GO_WORK_ENV) $(GO) test
RUNNABLE_FAST = GOPUS_TEST_TIER=fast $(GO_RUNNABLE_TEST)
RUNNABLE_PARITY = GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_RUNNABLE_TEST)
DNN_BLOB_DEFAULT_ROOT_RUN = 'Test(DefaultBuildDNNBlobKeepsDREDRuntimeDormant|DefaultBuildEncoderDNNBlobKeepsDREDDormant|HotPathAllocsDecodePLCDNNReadyAtMostBaseline|EncoderSetDNNBlobRetainedAcrossReset|DecoderSetDNNBlobRetainedAcrossReset|DecoderSetDNNBlobStereoRuntimeRetainedAcrossReset|MultistreamEncoderSetDNNBlobRetainedAcrossReset|MultistreamDecoderSetDNNBlobRetainedAcrossReset|ValidEncoderTestDNNBlobShape|ValidDecoderTestDNNBlobShape)'
DNN_BLOB_DEFAULT_MULTISTREAM_RUN = 'Test(DefaultBuildMultistreamDecoderRealBlobDormant|DefaultBuildMultistreamEncoderDNNBlobKeepsAllocsFlat)'
DEFAULT_DECODER_STATE_ROOT_RUN = 'Test(NewDecoder_DefaultMaxPacketLimits|Decode_ModeRouting|Decode_ExtendedFrameSizes|Decode_PLC_ModeTracking|Decoder_BandwidthAndLastPacketDuration)'
DEFAULT_DECODER_FEC_ROOT_RUN = 'Test(DecodeWithFEC_FallbackToPLC|DecodeWithFEC_CELTNoFEC|DecodeWithFEC_SILKStoresFEC|StoreFECData_ReusesBackingBuffer|DecodeFECFrame_BufferSizingUsesSingleFrame|DecodeWithFEC_HybridStoresFEC|DecodeWithFEC_Recovery|DecodeWithFEC_ResetClearsFEC)'
DEFAULT_DECODER_FEC_PACKET_ROOT_RUN = 'Test(DecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode|DecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity|DecodeWithFEC_ProvidedCELTPacketFallsBackToPLC|DecodeWithFEC_ProvidedCELTPacketClearsStoredFECState|DecodeWithFEC_ProvidedPacketUsesPacketModeForCELTGate|DecodeWithFEC_ProvidedPacketWithoutLBRRUsesDirectPLCFallback|DecodeWithFEC_PLCWithProvidedStateUsesProvidedMode|DecodeWithFEC_NoFECRequested)'
DEFAULT_MULTISTREAM_DECODER_STATE_ROOT_RUN = 'TestMultistreamDecoder(FirstPacketDurationMatrix|TracksPacketDurationChanges|EmptyPacketRejected)'
DEFAULT_WRAPPER_SAFETY_ROOT_RUN = 'Test(EncoderEncodeInt16RejectsOversizedInputWithoutPanic|MultistreamEncoderEncodeInt16RejectsOversizedInputWithoutPanic|DecoderResetClearsReportedState)'
DEFAULT_CONSTRUCTOR_VALIDATION_ROOT_RUN = 'Test(NewDecoder_(ValidParams|InvalidSampleRate|InvalidChannels)|NewEncoder_(ValidParams|InvalidParams|InvalidApplication)|SettingsForApplicationInvalidReturnsError|APIAbuseReturnsError)$$'
DEFAULT_HOTPATH_ALLOCS_ROOT_RUN = 'TestHotPathAllocs(EncodeFloat32|EncodeInt16|EncodeRestrictedSilkLowComplexity|DecodeFloat32|DecodeInt16|DecodePLC|DecodeStereo|StreamWriterFloat32)$$'
DEFAULT_ENCODER_ENCODE_ROOT_RUN = 'TestEncoder_(Encode_Float32|Encode_Int16|Encode_Int24|Encode_Int24_InvalidFrameSize|Encode_RoundTrip|Stereo|FrameSize|EncodeFloat32_Convenience|EncodeInt16Slice_Convenience|InvalidFrameSize_Encode|LongPacketRoundTrip)$$'
DEFAULT_ENCODER_CONTROLS_ROOT_RUN = 'TestEncoder_(SetBitrate|SetComplexity|SetBitrateMode|SetVBRAndConstraint|SetPacketLoss|SetBandwidth|SetApplication|RestrictedApplications|ExpertFrameDuration|SetSignal|SetMaxBandwidth|SetForceChannels|Lookahead|SetLSBDepth|SetPredictionDisabled|SetPhaseInversionDisabled)$$'
DEFAULT_DECODER_CONTROLS_ROOT_RUN = 'TestDecoder_(InDTX|SetGainBounds|IgnoreExtensions|GainAppliedToDecodeOutput|PitchGetter)$$'
DEFAULT_MULTISTREAM_CONTROLS_ROOT_RUN = 'TestMultistream(Encoder_(Controls|CVBRPacketEnvelope|SetApplicationPreservesControls|SetApplicationForwardsModeAndBandwidth|SetApplicationAfterEncodeRejected|RestrictedApplications|Lookahead)|Decoder_IgnoreExtensions)$$'
DEFAULT_STREAM_READER_ROOT_RUN = 'Test(NewReader_(ValidParams|InvalidParams)|Reader_(Format_Float32LE|Format_Int16LE|Reset|io_Reader_Interface))$$'
DEFAULT_STREAM_READER_FLOW_ROOT_RUN = 'TestReader_(Read_SinglePacket|LastGranulePos|Read_MultiplePackets|Read_PartialRead|Read_EOF|Read_PLC)$$'
DEFAULT_STREAM_WRITER_ROOT_RUN = 'Test(NewWriter_(ValidParams|InvalidParams)|Writer_(Format_Float32LE|Format_Int16LE|Flush|Flush_Empty|Close_FlushesAndClosesSink|Close_Idempotent|Reset|io_Writer_Interface))$$'
DEFAULT_STREAM_WRITER_FLOW_ROOT_RUN = 'TestWriter_(Write_SingleFrame|Write_MultipleFrames|Write_PartialFrame|Write_CrossFrameBoundary|WriteAfterClose|ResetAfterCloseReopensWriter|SinkShortWriteReturnsPartialProgress|SinkErrorAfterPartialWriteReturnsShortWrite|DTX)$$'
DEFAULT_STREAM_END_TO_END_ROOT_RUN = 'TestStream_(RoundTrip_Float32|RoundTrip_Int16|Pipe|LargeTransfer|io_Copy|MixedReadWrite)$$'
DEFAULT_OGG_READER_RUN = 'Test(NewReader_(Valid|NotOgg|NilReader|BadMagic)|Reader_(HeaderFields|MultistreamHeader|Serial|EmptyStream|GranulePos))$$'
DEFAULT_OGG_READER_FLOW_RUN = 'Test(Reader_(LargePacket|WriterRoundTrip|SeekGranule|SeekGranule_NotSeekable|Truncated)|ReadPacket_MultiPacketPageGranules)$$'
DEFAULT_OGG_WRITER_RUN = 'Test(NewWriter_(Mono|Stereo|InvalidChannels|NilWriter)|WriterWithConfig_(Multistream|PreservesMappingFamily|InvalidConfig)|WriterPreSkip|WriterOutputGain)$$'
DEFAULT_OGG_WRITER_FLOW_RUN = 'Test(WritePacket_(Single|ShortWriteRollsBackGranule|Multiple|LargePacket)|Close|WriterGranulePositionSequence)$$'
DEFAULT_OGG_PAGE_RUN = 'Test(OggCRC|BuildSegmentTable|ParseSegmentTable|SegmentTableRoundTrip|PageEncode|ParsePage|ParsePage_BadCRC|ParsePage_Truncated|PageFlags)$$'
DEFAULT_OGG_PAGE_PACKET_RUN = 'Test(PagePackets|PagePackets_LargePacket|PagePackets_MultiplePackets|PagePackets_Continuation|CRCCompatibility|PageRoundTrip_FullOggOpus|PageRoundTrip_AudioData|ParsePage_MultiplePages)$$'
DEFAULT_OGG_METADATA_RUN = 'Test(OpusHeadFamily0_Mono|OpusHeadFamily0_Stereo|OpusHeadFamily1|OpusHeadRoundTrip|OpusTags|OpusHeadErrors|OpusTagsErrors)$$'
DEFAULT_OGG_PROJECTION_RUN = 'TestDefaultProjectionDemixingMatrix(LibopusParity|FallbackIdentity)$$'
DEFAULT_OGG_INTERNAL_INTEGRATION_RUN = 'TestIntegration_(RoundTrip|ReaderWriterRoundTrip|GranulePosition|ContainerStructure|LargeFile)$$'
DEFAULT_PACKET_EXTENSION_DORMANCY_ROOT_RUN = 'Test(DecoderDecodeValidUnknownExtensionMatchesBasePacket|DecoderOpaquePaddingRemainsDecodableInDefaultBuild|DecoderCELTOpaquePaddingRemainsDecodable|DecoderCELTUnsupportedQEXTExtensionMatchesBasePacket)'
DEFAULT_PACKET_EXTENSION_MULTISTREAM_ROOT_RUN = 'Test(MultistreamPacketPadUnpadSelfDelimitedRoundTrip|MultistreamPacketPadUnpadThreeStreamsRoundTrip|MultistreamPacketPadRejectsInvalidSelfDelimited|MultistreamPacketUnpadRejectsInvalidSelfDelimited|MultistreamPacketPadRejectsLibopusParserEnvelopeViolation|MultistreamPacketUnpadRejectsLibopusParserEnvelopeViolation)'
DEFAULT_PACKET_EXTENSION_MULTISTREAM_RUN = 'Test(SelfDelimitedPacketPreservesPacketExtensions|DecodeSelfDelimitedPacketPreservesOpaqueMalformedPadding)'
DEFAULT_PACKET_PARSER_ROOT_RUN = 'Test(ParsePacketCode0|ParsePacketCode1|ParsePacketCode2|ParsePacketCode3CBR|ParsePacketCode3VBR|TwoByteFrameLength|ParsePacketErrors|ExtractFirstFramePayloadCode3VBROneFrameWithPadding|DecodeCode3VBROneFramePaddingRegression|ParsePacketRejectsLibopusEnvelopeViolations|ParsePacketCode3MaxFrames|ParsePacketCode3ContinuationPadding)'
DEFAULT_MULTISTREAM_PARSER_RUN = 'Test(ParseSelfDelimitedLength|ParseMultistreamPacket|GetFrameDuration|ValidateStreamDurations|ParseOpusPacketRejectsLibopusEnvelopeViolations|PacketDurationRejectsLibopusEnvelopeViolation|ParseMultistreamPacketWithSelfDelimitedCode3|SelfDelimitedPacketDropsOrdinaryPadding)'
DEFAULT_REPACKETIZER_ROOT_RUN = 'Test(RepacketizerParityWithLibopusFixture|RepacketizerRejectsTOCMismatch|RepacketizerRejectsDurationOver120ms|RepacketizerRejectsLibopusParserEnvelopeViolations)'
UNSUPPORTED_CONTROLS_CORE_ROOT_RUN = 'Test(SupportsOptionalExtension|UnsupportedControlsBuildExposesQuarantinedTopLevelControls|UnsupportedControlsBuildPublicAPIContract|PublicDRED|DREDDecoderParseRequiresModel|DREDDecoderParseAndProcessRetainsMetadata|DREDDecoderParseClearsStateWhenPacketHasNoDRED|DREDDecoderParseClearsStateOnMalformedPacket|StandaloneDREDParseMatchesLibopus|StandaloneDREDProcessMatchesLibopusOnRealPacket|StandaloneDREDProcessLifecycleMatchesLibopusOnRealPacket|StandaloneDREDRecoveryWindowMatchesLibopus|StandaloneDREDRecoveryQueueMatchesLibopus|DecoderCoreDNNBlobDoesNotArmGoodPacketDREDWork|DecoderCachedDREDRecoveryMatchesLibopusLifecycle|DecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELT|DecoderCachedDREDRecoveryMatchesLibopusLifecycle48kHybrid|DecoderCachedDREDRecoveryCursorAdvancesAcrossLosses|DecoderCachedDREDRecoveryCursorAdvancesAcrossLosses48kCELT|DecoderCachedDREDRecoveryCursorStaysIdleAcrossLosses48kHybrid|DecoderExplicitDREDWarmup48kStateMatchesLibopus)|ExampleSupportsOptionalExtension'
DRED_STEREO_RECOVERY_ROOT_RUN = 'Test(DecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELTStereo|DecoderCachedDREDRecoveryCursorAdvancesAcrossLosses48kCELTStereo|DecoderCachedDREDRecoveryCursorStaysIdleAcrossLosses48kHybridStereo)'
DRED_LATENTS_TRACE_RUN = 'TestLibopusDREDLatentsTraceStereoDivergesFromMono'
DRED_INTERNAL_CORE_RUN = 'Test(CacheStoreResultAndClear|CacheStoreRejectsSmallBuffer|CacheInvalidateDropsVisibilityWithoutFullClear|DecodedInvalidateDropsFeatureVisibilityWithoutFullClear|EncoderBufferReset|EncoderBufferAppend16kWithoutEmission|EncoderBufferAppend16kEmitsOneFrameAndRetainsTail|EncoderBufferAppend16kEmitsTwoFramesAndAdvancesLatentOffset|UpdateActivityHistory|EncodeExperimentalPayloadHasExpectedHeader|EncodeExperimentalPayloadDoesNotAllocate|HeaderAvailability|HeaderAvailabilityClampsNegative|ValidExperimentalPayload|ResultFeatureWindowRecoverable|ResultFeatureWindowMissingPositiveAndNegative|QueueProcessedFeaturesDoesNotAllocate|ProcessedFeatureWindowUsesDecodedLatents|ParseHeader|ParseHeaderWithExtraOffsetAndFrameOffset|ParseHeaderRejectsEmptyPayload|ParsedForRequest|ParsePayload|RequestedFeatureFrames|MaxLatentsForRequest|HeaderFillQuantizerLevels|HeaderMaxAvailableSamples)'
DRED_DECODER_DORMANCY_ROOT_RUN = 'Test(DecoderCoreDNNBlobDoesNotArmGoodPacketDREDWork|NewDecoderLeavesDREDPayloadBufferDormant|StandaloneDREDArmKeepsRecoveryNeuralAnd48kBridgeDormant|MainDecoderDNNBlobKeepsRecoveryAndPayloadDormant|MainDecoder48kDNNBlobKeepsRecoveryAndBridgeDormant|MainDecoder16kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss|MainDecoder48kDNNBlobGoodDecodeKeepsRecoveryDormantUntilLoss|MainDecoder48kDNNBlobGoodSILKHybridDecodeKeepsRecoveryDormantUntilLoss|ClearingStandaloneDREDPreservesMainNeuralState|DecoderCachesDREDPayloadWhenDREDModelLoaded|DecoderCachesDREDSampleTimingForLaterFrame|DecoderLeavesDREDPayloadDormantWithoutDREDModel|DecoderLeavesDREDStateDormantWithoutAnySidecar|PublicDREDDecoderSetDNNBlobArmsDREDDecoderWhenBlobContainsModel|DecoderLeavesDREDPayloadDormantWhenIgnoringExtensions|DecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions|DecoderResetDropsActivatedDREDRuntimeBackToDormant)'
DRED_DECODER_RECOVERY_INTERNAL_ROOT_RUN = 'Test(DecoderDREDRecoveryBlendFollowsLifecycle|DecoderMarkDREDUpdatedPCMRefreshesNeuralHistory|DecoderMarkDREDUpdatedPCMCELTKeepsBridgeOwnedHistory|DecoderMarkDREDUpdatedPCMDormantWithoutSidecar|DecoderMarkDREDUpdatedPCMDoesNotTrackHistoryWithoutNeuralConcealment|DecoderPrimeDREDCELTEntryHistoryUsesCELTBridge|DecoderPrimeDREDCELTEntryHistoryStaysDormantWithoutNeuralConcealment|DecoderDecodePLCAppliesNeuralConcealmentWhenReady)'
DRED_ENCODER_RUNTIME_INTERNAL_RUN = 'Test(EncoderProcessDREDLatentsDownmixesStereo16k|EncoderProcessDREDLatentsSupportsRateConversion|EncoderProcessDREDLatentsBuffers48k10msFrames|EncoderProcessDREDLatentsTracksHistoryWindow|EncoderProcessDREDLatentsTracksOffsets|EncoderBuildDREDExperimentalPayloadUsesRuntimeHistory|EncoderBuildDREDExperimentalPayloadDoesNotAllocate|EncoderSetDREDDurationGrowsScratchPacketWhenArmed)'
DRED_PAYLOAD_PARSER_ROOT_RUN = 'TestFindDREDPayload'
DRED_QUALITY_ROOT_RUN = 'TestExplicitDREDImprovesConcealedAudioQualityAtSixtyPercentLoss'
DRED_MULTISTREAM_DORMANCY_RUN = 'Test(NewDecoderLeavesDREDSidecarDormant|DecoderCachesDREDPayloadPerStreamWhenModelLoaded|DecoderCachesDREDSampleTimingForLaterStreamFrame|DecoderLeavesDREDPayloadDormantWithoutDREDModel|DecoderLeavesDREDStateDormantWithoutAnySidecar|DecoderLeavesDREDStateDormantWithOnlyMainDNNBlob|DecoderLeavesDREDPayloadDormantWhenIgnoringExtensions|DecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions|DecoderDoesNotCachePartialDREDStateWhenLaterStreamFails|DREDBuildTagExposesEncoderControlsOnlyReadyRequiresEveryStream)'
UNSUPPORTED_CONTROLS_MULTISTREAM_DORMANCY_RUN = 'Test(NewDecoderLeavesDREDSidecarDormant|DecoderCachesDREDPayloadPerStreamWhenModelLoaded|DecoderCachesDREDSampleTimingForLaterStreamFrame|DecoderLeavesDREDPayloadDormantWithoutDREDModel|DecoderLeavesDREDStateDormantWithoutAnySidecar|DecoderLeavesDREDStateDormantWithOnlyMainDNNBlob|DecoderLeavesDREDPayloadDormantWhenIgnoringExtensions|DecoderDREDCacheFollowsStandaloneModelAndIgnoreExtensions|DecoderDoesNotCachePartialDREDStateWhenLaterStreamFails|EncoderDREDReadyRequiresModelAndDurationOnEveryStream)'
DRED_MULTISTREAM_RECOVERY_INTERNAL_RUN = 'TestDecoderDREDRecoveryBlendFollowsLifecycle'
QEXT_PACKET_EXTENSION_ROOT_RUN = 'Test(GeneratePacketExtensionsMatchesLibopusCases|PacketExtensionIteratorParseAndCount|PacketExtensionIteratorRepeatExpansion|PacketExtensionIteratorRejectsInvalidSeparator|MultistreamPacketPadUnpadSelfDelimitedRoundTrip|MultistreamPacketPadUnpadThreeStreamsRoundTrip|RepacketizerPreservesPacketExtensions|PacketPadPreservesPacketExtensions|SelfDelimitedPacketPreservesPacketExtensions|DecodeSelfDelimitedPacketPreservesOpaqueMalformedPadding)'
QEXT_PACKET_EXTENSION_MULTISTREAM_RUN = 'Test(SelfDelimitedPacketPreservesPacketExtensions|DecodeSelfDelimitedPacketPreservesOpaqueMalformedPadding)'
QEXT_LIBOPUS_TOOLING_RUN = 'TestFindQEXTLibopusToolForOSUsesSeparateSourceTree'
UNSUPPORTED_CONTROLS_PARITY_ROOT_RUN = 'Test(ParsedDREDAvailabilityMatchesLibopus)'
UNSUPPORTED_CONTROLS_PARITY_SILK_DRED_ROOT_RUN = 'Test(DecoderExplicitSILKDREDDecodeMatchesLibopus|DecoderExplicit16kSILKDREDDecodeMatchesLibopus)'
UNSUPPORTED_CONTROLS_PARITY_DECODER_ROOT_RUN = 'Test(DecoderCachedDREDDecodeMatrixMatchesLiveSequenceOracle|DecoderCachedStereoDREDDecodeMatchesLiveSequenceOracle|DecoderCachedStereoDREDCELTMatrixMatchesLiveSequenceOracle|DecoderCachedStereoDREDHybridMatrixMatchesLiveSequenceOracle|DecoderCachedStereoDREDSecondLossMatchesLiveSequenceOracle|DecoderCachedStereoDREDThenNextPacketMatchesLiveSequenceOracle|DecoderCachedStereoDREDSecondLossThenNextPacketMatchesLiveSequenceOracle|DecoderCachedDREDDecodeCELTSuperwidebandMatrixMatchesLiveSequenceOracle|DecoderCachedDREDDecodeCELTWidebandMatrixMatchesLiveSequenceOracle|DecoderCachedDREDThenNextPacketMatchesLiveSequenceOracle|DecoderCachedDREDThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle|DecoderCachedDREDThenNextPacketCELTWidebandMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossCELTSuperwidebandMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossCELTWidebandMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossThenNextPacketMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossThenNextPacketCELTSuperwidebandMatchesLiveSequenceOracle|DecoderCachedDREDSecondLossThenNextPacketCELTWidebandMatchesLiveSequenceOracle|DecoderCachedHybridDREDDecodeMatrixMatchesLiveSequenceOracle|DecoderCachedHybridDRED16kDecodeMatrixMatchesLiveSequenceOracle|DecoderCachedHybridDREDThenNextPacketMatchesLiveSequenceOracle|DecoderCachedHybridDRED16kThenNextPacketMatchesLiveSequenceOracle|DecoderCachedHybridDREDSecondLossMatchesLiveSequenceOracle|DecoderCachedHybridDRED16kSecondLossMatchesLiveSequenceOracle|DecoderCachedHybridSecondLossThenNextPacketMatchesLiveSequenceOracle|DecoderCachedHybridDRED16kSecondLossThenNextPacketMatchesLiveSequenceOracle|DecoderFirstLossNeuralConcealmentMatchesLiveSequenceOracle|DecoderFirstLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle|DecoderFirstLossThenNextPacketMatchesLiveSequenceOracle|DecoderFirstLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle|DecoderSecondLossNeuralConcealmentMatchesLiveSequenceOracle|DecoderSecondLossNeuralConcealment16kFrameSizeMatrixMatchesLiveSequenceOracle|DecoderSecondLossThenNextPacketMatchesLiveSequenceOracle|DecoderSecondLossThenNextPacket16kFrameSizeMatrixMatchesLiveSequenceOracle|DecoderExplicitDREDCELT48kBridgeMatchesLibopusFirstLoss|DecoderExplicitDREDCELT48kBridgeMatchesLibopusSecondLoss|DecoderExplicitDREDDecodeMatchesLibopus|DecoderExplicitStereoDREDDecodeMatchesLibopus|DecoderExplicitStereoDRED16kDecodeMatchesLibopus|DecoderExplicitStereoHybridDRED16kDecodeMatchesLibopus|DecoderExplicitSILKDREDDecodeStereoMatchesLibopus|DecoderExplicitDREDDecode16kMatchesLibopus|DecoderExplicitDREDDecode16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecode16kCELTWidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeSecondLossMatchesLibopus|DecoderExplicitDREDDecodeSecondLossGainTransitionMatchesLibopus|DecoderExplicitDREDDecodeSecondLoss16kMatchesLibopus|DecoderExplicitDREDDecodeOffsetMatrixMatchesLibopus|DecoderExplicitDREDDecodeOffsetMatrixCELTSuperwidebandMatchesLibopus|DecoderExplicitDREDDecodeOffsetMatrixHybridSuperwidebandMatchesLibopus|DecoderExplicitDREDDecodeOffsetMatrixHybridFullbandMatchesLibopus|DecoderExplicitDREDDecodeOffsetMatrix16kHybridMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacketMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacket16kMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacket16kFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacket16kCELTSuperwidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacket16kCELTWidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitHybridDREDDecodeMatrixMatchesLibopus|DecoderExplicitHybridDREDDecode16kMatrixMatchesLibopus|DecoderExplicit16kHybridDREDDecodeMatrixMatchesLibopus|DecoderExplicitHybridDREDDecodeThenNextPacketMatchesLibopus|DecoderExplicitHybridDREDDecodeThenNextPacket16kMatchesLibopus|DecoderExplicitHybridDREDDecodeSecondLossMatrixMatchesLibopus|DecoderExplicitHybridDREDDecodeSecondLoss16kMatrixMatchesLibopus|DecoderExplicitHybridDREDDecodeSecondLossThenNextPacketMatrixMatchesLibopus|DecoderExplicitHybridDREDDecodeSecondLossThenNextPacket16kMatrixMatchesLibopus|DecoderExplicitSecondLossThenNextPacketMatchesLibopus|DecoderExplicitSecondLossThenNextPacket16kMatchesLibopus|DecoderExplicitDREDDecodeFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeCELTSuperwidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeCELTWidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecode16kFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeSecondLossFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeSecondLossCELTSuperwidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeSecondLossCELTWidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacketFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacketCELTSuperwidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitDREDDecodeThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus|DecoderExplicitSecondLossThenNextPacketFrameSizeMatrixMatchesLibopus|DecoderExplicitSecondLossThenNextPacketCELTWidebandFrameSizeMatrixMatchesLibopus)'

# Run golangci-lint
lint:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: GOWORK=off go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; exit 1; }
	$(GO_WORK_ENV) $(GOLANGCI_LINT) run ./...

# Run golangci-lint with auto-fix
lint-fix:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "golangci-lint not found. Install with: GOWORK=off go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)"; exit 1; }
	$(GO_WORK_ENV) $(GOLANGCI_LINT) run --fix ./...

# Run tests
test:
	$(GO_RUNNABLE_TEST)

# Fast inner-loop tests (skips parity/exhaustive tier checks)
test-fast:
	$(GO_RUNNABLE_TEST) -short

# Race detector sweep across all packages at fast test tier (keeps runtime bounded).
test-race:
	$(RUNNABLE_FAST) -race -count=1 -timeout=20m

# Fuzz smoke run for packet/fixture parsers.
test-fuzz-smoke:
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketExtensionIterator_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketMutationHelpers_NoPanic' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=$(GOPUS_FUZZ_SMOKE_FUZZTIME) -count=1

# Safety-focused fuzzing for malformed packets, Ogg pages, and libopus differential decode.
test-fuzz-safety: ensure-libopus
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzParsePacket_NoPanic' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketExtensionIterator_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzPacketMutationHelpers_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test . -run='^$$' -fuzz='FuzzDecodeNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run='^$$' -fuzz='FuzzFindDREDPayload_NoPanic' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run='^$$' -fuzz='FuzzOggReaderNeverPanics' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzParseOpusDemoBitstream' -fuzztime=$(GOPUS_SAFETY_PARSER_FUZZTIME) -count=1
	$(GO_WORK_ENV) $(GO) test ./testvectors -run='^$$' -fuzz='FuzzDecodeAgainstLibopus' -fuzztime=$(GOPUS_SAFETY_FUZZTIME) -count=1

# Downstream consumer smoke path from a nested external module boundary.
test-consumer-smoke:
	cd examples/external-consumer-smoke && $(GO_WORK_ENV) $(GO) test ./... -count=1

# Compile and test maintained examples, including build-tag-only surfaces.
test-examples-smoke:
	$(GO_WORK_ENV) $(GO) test ./examples/... -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_dred ./examples/... -count=1
	$(GO_WORK_ENV) $(GO) test -tags gopus_qext ./examples/... -count=1
	tmp="$$(mktemp -d)" && trap 'rm -rf "$$tmp"' EXIT && cd examples/webrtc-control && $(GO_WORK_ENV) $(GO) build -o "$$tmp/webrtc-control" .
	cd examples/webrtc-dred-loopback && $(GO_WORK_ENV) $(GO) test ./... -count=1
	cd examples/webrtc-dred-loopback && $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./... -count=1

# Lightweight docs and optional-extension contract that keeps release-surface claims aligned.
test-doc-contract:
	$(GO_WORK_ENV) $(GO) test . -run 'Test(OptionalExtensionDocsContract|TrustDocsContract|TrustSensitiveFilesHaveCodeOwners|ReleaseNotesExistForTags|DefaultBuildPublicAPIContract|Encoder_OptionalExtensionControls|Decoder_OptionalExtensionControls|MultistreamEncoder_OptionalExtensionControls|MultistreamDecoder_OptionalExtensionControls|SupportsOptionalExtension)|ExampleSupportsOptionalExtension' -count=1
	$(GO_WORK_ENV) $(GO) test . ./multistream -run 'TestDefaultBuildQuarantinesUnsupportedControls' -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_DECODER_STATE_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_DECODER_FEC_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_DECODER_FEC_PACKET_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_MULTISTREAM_DECODER_STATE_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_WRAPPER_SAFETY_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_CONSTRUCTOR_VALIDATION_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_HOTPATH_ALLOCS_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_ENCODER_ENCODE_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_ENCODER_CONTROLS_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_DECODER_CONTROLS_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_MULTISTREAM_CONTROLS_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_STREAM_READER_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_STREAM_READER_FLOW_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_STREAM_WRITER_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_STREAM_WRITER_FLOW_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_STREAM_END_TO_END_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_READER_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_READER_FLOW_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_WRITER_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_WRITER_FLOW_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_PAGE_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_PAGE_PACKET_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_METADATA_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_PROJECTION_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./container/ogg -run $(DEFAULT_OGG_INTERNAL_INTEGRATION_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_PACKET_EXTENSION_DORMANCY_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_PACKET_EXTENSION_MULTISTREAM_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./multistream -run $(DEFAULT_PACKET_EXTENSION_MULTISTREAM_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_PACKET_PARSER_ROOT_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test ./multistream -run $(DEFAULT_MULTISTREAM_PARSER_RUN) -count=1
	$(GO_WORK_ENV) $(GO) test . -run $(DEFAULT_REPACKETIZER_ROOT_RUN) -count=1

# Default-supported DNN blob control parity against libopus USE_WEIGHTS_FILE
# model blobs. The target fails if the required libopus-backed test is skipped.
test-dnn-blob-parity: ensure-libopus
	@json_out="$$(mktemp)"; \
	json_part="$$json_out.part"; \
	trap 'rm -f "$$json_out" "$$json_part"' EXIT; \
	run_json() { \
		if ! "$$@" -json > "$$json_part"; then \
			cat "$$json_part"; \
			cat "$$json_part" >> "$$json_out"; \
			exit 1; \
		fi; \
		cat "$$json_part"; \
		cat "$$json_part" >> "$$json_out"; \
		: > "$$json_part"; \
	}; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test . -run 'Test(DNNBlobControlAcceptsLibopusModelBlobs|SupportsOptionalExtension)|ExampleSupportsOptionalExtension' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test . -run $(DNN_BLOB_DEFAULT_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test ./multistream -run $(DNN_BLOB_DEFAULT_MULTISTREAM_RUN) -count=1; \
	if grep -q '"Action":"skip"' "$$json_out"; then \
		echo "Unexpected skip detected in required DNN blob parity gate:"; \
		grep '"Action":"skip"' "$$json_out"; \
		exit 1; \
	fi

# Supported DRED feature-tag smoke. The unsupported-controls tag remains a
# quarantine umbrella; this target verifies the supported DRED surface by itself.
test-dred-tag: ensure-libopus
	@json_out="$$(mktemp)"; \
	json_part="$$json_out.part"; \
	trap 'rm -f "$$json_out" "$$json_part"' EXIT; \
	run_json() { \
		if ! "$$@" -json > "$$json_part"; then \
			cat "$$json_part"; \
			cat "$$json_part" >> "$$json_out"; \
			exit 1; \
		fi; \
		cat "$$json_part"; \
		cat "$$json_part" >> "$$json_out"; \
		: > "$$json_part"; \
	}; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(OptionalExtensionDocsContract|SupportsOptionalExtension|DREDBuildTagExposesSupportedTopLevelControls|DREDBuildPublicAPIContract|PublicDRED|Encoder_OptionalExtensionControls|MultistreamEncoder_OptionalExtensionControls)|ExampleSupportsOptionalExtension' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(DREDDecoderParseRequiresModel|DREDDecoderParseClearsStateWhenPacketHasNoDRED|DREDDecoderProcessRejectsEmptyState|DREDDecoderProcessDoesNotAllocate|DREDDecoderParseAndProcessDoesNotAllocate|DREDDecoderParseClearsStateOnMalformedPacket)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(DREDDecoderParseAndProcessRetainsMetadata|StandaloneDREDParseMatchesLibopus)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(StandaloneDREDProcessMatchesLibopusOnRealPacket|StandaloneDREDProcessLifecycleMatchesLibopusOnRealPacket)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(StandaloneDREDRecoveryWindowMatchesLibopus|StandaloneDREDRecoveryQueueMatchesLibopus)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run $(DRED_PAYLOAD_PARSER_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./internal/dred -run $(DRED_INTERNAL_CORE_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run $(DRED_DECODER_DORMANCY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run $(DRED_DECODER_RECOVERY_INTERNAL_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run $(DRED_QUALITY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(DecoderCachedDREDRecoveryMatchesLibopusLifecycle|DecoderCachedDREDRecoveryMatchesLibopusLifecycle48kCELT|DecoderCachedDREDRecoveryMatchesLibopusLifecycle48kHybrid|DecoderCachedDREDRecoveryCursorAdvancesAcrossLosses|DecoderCachedDREDRecoveryCursorAdvancesAcrossLosses48kCELT|DecoderCachedDREDRecoveryCursorStaysIdleAcrossLosses48kHybrid)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run $(DRED_STEREO_RECOVERY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred . -run 'Test(ProbeEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo|EncoderSilkMono20msPrimaryFrameByteExactMatchesLibopus|EncoderSilkMono40msPrimaryFrameByteExactMatchesLibopus|EncoderSilkMono60msPrimaryFrameByteExactMatchesLibopus|EncoderSilkStereo20msPrimaryFrameByteExactMatchesLibopus|EncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msPayloadOnly|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo|MultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo|MultistreamEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereoPayloadOnly|MultistreamEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./encoder -run 'Test(DREDRuntimeBuildExposesEncoderControls|EncoderDREDDuration|EncoderResetClearsDREDDuration|EncoderDREDReadyRequiresModelAndDuration|EncoderDREDRuntimeStaysDormantUntilReady|EncoderDREDEncodingActiveRequiresModelAndDuration|EncoderEncodeKeepsDREDRuntimeDormantUntilDurationArmed|EncoderProcessDREDLatentsDoesNotAllocate|EncoderProcessDREDLatentsDoesNotAllocate48k|MaybeBuildSingleFrameDREDPacketCarriesExtension)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./encoder -run $(DRED_ENCODER_RUNTIME_INTERNAL_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./multistream -run $(DRED_MULTISTREAM_DORMANCY_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./multistream -run $(DRED_MULTISTREAM_RECOVERY_INTERNAL_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_dred ./multistream -run 'Test(DREDBuildTagExposesEncoderControlsOnly|DecoderPublicSetDNNBlobArmsDREDDecoderWhenBlobContainsModel|DecoderDecodeNilConsumesMultistreamDREDNeuralStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondCoupledStream)' -count=1; \
	if grep -q '"Action":"skip"' "$$json_out"; then \
		echo "Unexpected skip detected in required DRED tag gate:"; \
		grep '"Action":"skip"' "$$json_out"; \
		exit 1; \
	fi

# Supported QEXT feature-tag parity. The default build keeps QEXT controls
# absent and leaves packet-extension payload plumbing behind compile-time gates.
test-qext-parity: ensure-libopus-qext
	@json_out="$$(mktemp)"; \
	json_part="$$json_out.part"; \
	trap 'rm -f "$$json_out" "$$json_part"' EXIT; \
	run_json() { \
		if ! "$$@" -json > "$$json_part"; then \
			cat "$$json_part"; \
			cat "$$json_part" >> "$$json_out"; \
			exit 1; \
		fi; \
		cat "$$json_part"; \
		cat "$$json_part" >> "$$json_out"; \
		: > "$$json_part"; \
	}; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_qext . -run 'Test(OptionalExtensionDocsContract|SupportsOptionalExtension|QEXTBuildPublicAPIContract|QEXTBuildTagExposesSupportedTopLevelControls|Encoder_OptionalExtensionControls|MultistreamEncoder_OptionalExtensionControls|DecodeGopusQEXTPacketMatchesLibopus|DecodeLibopusQEXTPacketMatchesLibopus|DecodeLibopusQEXTPacketFinalRangeMatchesLibopus|DecodeLibopusQEXTPacketIgnoreExtensionsMatchesInactiveCELT|DecodeLibopusQEXTOpaquePaddingMatchesInactiveCELT|DecodeLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads|DecodeLibopusQEXTMultiFramePacketMatchesExplicitPayloads|DecodeLibopusQEXTMultiFrameIgnoreExtensionsMatchesInactivePayloads|DecodeLibopusQEXTPacketCELTFloat32FastPathMatchesFloat64|DecodeLibopusQEXTPacketWrapperMatchesDirectCELT|DecodeHybridLibopusQEXTPacketMatchesLibopus|DecodeHybridLibopusQEXTPacketIgnoreExtensionsMatchesInactiveHybrid|DecodeHybridLibopusQEXTOpaquePaddingMatchesInactiveHybrid|DecodeHybridLibopusQEXTIgnoreExtensionsToggleSequenceMatchesExplicitPayloads|DecodeStereoLibopusQEXTPacketToMonoMatchesLibopus|DecodeLibopusRestrictedCELTPacketMatchesLibopus|DecodeLibopusQEXTChannelTransitionSequenceMatchesLibopus)|ExampleSupportsOptionalExtension' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_qext . -run $(QEXT_PACKET_EXTENSION_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test ./internal/libopustooling -run $(QEXT_LIBOPUS_TOOLING_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_qext ./encoder ./celt ./multistream -run 'QEXT' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_qext ./multistream -run $(QEXT_PACKET_EXTENSION_MULTISTREAM_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags 'gopus_dred gopus_qext' . -run 'Test(CombinedDREDQEXTBuildOptionalExtensionContract|SupportsOptionalExtension)|ExampleSupportsOptionalExtension' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags 'gopus_unsupported_controls gopus_qext' . -run 'Test(QEXTUnsupportedControlsBuildOptionalExtensionContract|SupportsOptionalExtension)|ExampleSupportsOptionalExtension' -count=1; \
	if grep -q '"Action":"skip"' "$$json_out"; then \
		echo "Unexpected skip detected in required QEXT parity gate:"; \
		grep '"Action":"skip"' "$$json_out"; \
		exit 1; \
	fi

# Quarantine build smoke for unsupported controls that should never leak into the default surface.
test-unsupported-controls-tag: ensure-libopus
	@json_out="$$(mktemp)"; \
	json_part="$$json_out.part"; \
	trap 'rm -f "$$json_out" "$$json_part"' EXIT; \
	run_json() { \
		if ! "$$@" -json > "$$json_part"; then \
			cat "$$json_part"; \
			cat "$$json_part" >> "$$json_out"; \
			exit 1; \
		fi; \
		cat "$$json_part"; \
		cat "$$json_part" >> "$$json_out"; \
		: > "$$json_part"; \
	}; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(UNSUPPORTED_CONTROLS_CORE_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_PAYLOAD_PARSER_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_DECODER_DORMANCY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_DECODER_RECOVERY_INTERNAL_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_QUALITY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_STEREO_RECOVERY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./encoder ./multistream -run 'Test(DREDRuntimeBuildExposesEncoderControls|EncoderDREDDuration|EncoderResetClearsDREDDuration|EncoderDREDReadyRequiresModelAndDuration|EncoderDREDEncodingActiveRequiresModelAndDuration|EncoderEncodeKeepsDREDRuntimeDormantUntilDurationArmed)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./encoder -run $(DRED_ENCODER_RUNTIME_INTERNAL_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./multistream -run $(UNSUPPORTED_CONTROLS_MULTISTREAM_DORMANCY_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./multistream -run $(DRED_MULTISTREAM_RECOVERY_INTERNAL_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./multistream -run 'Test(DecoderPublicSetDNNBlobArmsDREDDecoderWhenBlobContainsModel|DecoderDecodeNilConsumesMultistreamDREDNeuralStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondCoupledStream)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/dred -run 'Test(EncodeExperimentalPayloadMatchesLibopus|EncodeExperimentalPayloadMatchesLibopusDelayedOffset)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/dred -run $(DRED_LATENTS_TRACE_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/lpcnetplc -run 'Test(PredictorMatchesLibopusOnRealModel|FARGANConditionerMatchesLibopusOnRealModel|FARGANPrimeContinuityMatchesLibopusOnRealModel|FARGANSynthesizeMatchesLibopusOnRealModel|MarkUpdatedFrameFloatMatchesLibopus|PrefillAndConcealmentFeatureStepMatchLibopus|BoundedConcealFrameFloatMatchesLibopus|ConcealFrameFloatWithAnalysisMatchesLibopusColdStart)' -count=1; \
	if grep -q '"Action":"skip"' "$$json_out"; then \
		echo "Unexpected skip detected in required unsupported-controls tag gate:"; \
		grep '"Action":"skip"' "$$json_out"; \
		exit 1; \
	fi

# Required tag-gated DRED parity sweep. Keep it separate from the quarantine API
# smoke so support claims stay seam-scoped.
test-unsupported-controls-parity: ensure-libopus
	@json_out="$$(mktemp)"; \
	json_part="$$json_out.part"; \
	trap 'rm -f "$$json_out" "$$json_part"' EXIT; \
	run_json() { \
		if ! "$$@" -json > "$$json_part"; then \
			cat "$$json_part"; \
			cat "$$json_part" >> "$$json_out"; \
			exit 1; \
		fi; \
		cat "$$json_part"; \
		cat "$$json_part" >> "$$json_out"; \
		: > "$$json_part"; \
	}; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(UNSUPPORTED_CONTROLS_PARITY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run 'Test(OSCEBWEForwardPassMatchesLibopusNumericalParity|OSCEBWERawSignalNetMatchesLibopus|OSCEBWEForwardPassPLCContinuityMatchesLibopus|OSCEBWECrossFade10msMatchesLibopus|OSCEBWEModelForwardPassMatchesLibopus|OSCEBWEInt8LibopusKernelParity|OSCEBWEForwardPassInt8KernelReproducible|OSCELACEForwardPassMatchesLibopus|DecoderOSCEBWERuntimeIntegration|DecoderOSCELACERuntimeIntegration|DecoderOSCEBWECrossFadeTransition|DecoderOSCEBWEPLC|DecoderOSCELACECrossFadeTransition|DecoderOSCELACEPLC|MultistreamDecoderOSCEBWELACERuntimeIntegration|MultistreamDecoderOSCEBWEMatchesSingleStreamDecoder)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./multistream -run 'Test(DecoderDecodeNilConsumesMultistreamDREDNeuralStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKNonFinalMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondMonoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralCELTFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralHybridFinalSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalStereoStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKSecondCoupledStream|DecoderDecodeNilConsumesMultistreamDREDNeuralSILKFinalSecondCoupledStream)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/dred -run 'Test(ConvertTo16kMonoFloat64MatchesLibopus|ConvertTo16kMonoFloat64MatchesLibopusAcrossCalls|EncodeExperimentalPayloadMatchesLibopusLargeLaplaceContinuation|RDOVAEEncoderMatchesLibopusOnRealModel)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/dred -run $(DRED_LATENTS_TRACE_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls ./internal/lpcnetplc -run 'Test(LPCNetSingleFrameFeaturesFloatMatchesLibopusColdStart|LPCNetSingleFrameFeaturesFloatMatchesLibopusStatefulSequence|BurgCepstralAnalysisMatchesLibopus|PitchDNNMatchesLibopusOnRealModel|ConcealFrameFloatWithAnalysisMatchesLibopus)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run 'Test(ProbeEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo|EncoderSilkMono20msPrimaryFrameByteExactMatchesLibopus|EncoderSilkMono40msPrimaryFrameByteExactMatchesLibopus|EncoderSilkMono60msPrimaryFrameByteExactMatchesLibopus|EncoderSilkStereo20msPrimaryFrameByteExactMatchesLibopus|EncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msPayloadOnly|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo|EncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo|EncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo|MultistreamEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo|MultistreamEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereoPayloadOnly|MultistreamEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run 'Test(DecoderExplicitDREDFirstConcealFrameBootstraps48kRuntime|DecoderExplicitDREDThreeConcealFramesBootstraps48kRuntime|DecoderExplicitDREDThreeConcealFramesManualStep48kRuntime|DecoderExplicitDREDThreeConcealFramesMixedHelpers48kRuntime)' -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(DRED_STEREO_RECOVERY_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(UNSUPPORTED_CONTROLS_PARITY_SILK_DRED_ROOT_RUN) -count=1; \
	run_json env GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 $(GO_WORK_ENV) $(GO) test -tags gopus_unsupported_controls . -run $(UNSUPPORTED_CONTROLS_PARITY_DECODER_ROOT_RUN) -count=1; \
	if grep -q '"Action":"skip"' "$$json_out"; then \
		echo "Unexpected skip detected in required unsupported-controls parity gate:"; \
		grep '"Action":"skip"' "$$json_out"; \
		exit 1; \
	fi

# Legacy alias for older automation; Hybrid DRED packet-shape exactness now
# lives in the required supported-tag and quarantine parity gates.
test-unsupported-controls-parity-experimental: test-unsupported-controls-parity

# Primary libopus-facing focused gate.
test-quality: ensure-testvectors
	$(GO_TEST_PARITY) ./testvectors -run 'TestFinalRangeVerification|TestEncoderComplianceSummary|TestEncoderCompliancePrecisionGuard|TestEncoderVariantProfileParityAgainstLibopusFixture|TestEncoderVariantCELTAllocationParityAgainstFixture|TestEncoderVariantCELTHeaderParityAgainstFixture|TestDecoderParityLibopusMatrix|TestDecoderLossParityLibopusFixture|TestDecoderHybridToCELT10msTransitionParity|TestDecoderHybridToCELT20msTransitionParity' -count=1 -v

# Optional libopus-internal exactness checks. These are intentionally not part
# of the default production gate so math optimizations can move while quality
# and interoperability stay enforced.
test-exactness:
	GOPUS_TEST_TIER=fast GOPUS_LIBOPUS_EXACTNESS=1 $(GO_WORK_ENV) $(GO) test ./encoder -run 'TestModeFixtureParityWithLibopus|TestAnalysisFixtureParityWithLibopus' -count=1

# Compact markdown summary for the quality + compatibility gates.
quality-report: ensure-libopus
	$(GO_WORK_ENV) $(GO) run ./tools/qualityreport -out-dir $(QUALITY_REPORT_DIR)

# Native assembly/fallback validation matrix.
test-assembly-safety: ensure-libopus
	$(ASSEMBLY_SAFETY_MATRIX)

# Long-running randomized encode/decode corruption soak.
test-soak-safety:
	$(GO_WORK_ENV) $(GO) run ./tools/safety_soak -duration $(GOPUS_SAFETY_SOAK_DURATION) -report-interval $(GOPUS_SAFETY_SOAK_REPORT_INTERVAL) -max-rss-growth-mib $(GOPUS_SAFETY_SOAK_MAX_RSS_GROWTH_MIB) -max-goroutine-growth $(GOPUS_SAFETY_SOAK_MAX_GOROUTINE_GROWTH) -max-hotpath-allocs $(GOPUS_SAFETY_SOAK_MAX_ALLOCS)

# Hot-path performance guardrail checks (median benchmark thresholds + alloc bounds).
bench-guard:
	$(GO_WORK_ENV) $(GO) run ./tools/benchguard -config tools/bench_guardrails.json

# Libopus-relative codec performance guardrails against the pinned reference.
bench-libopus-guard: bench-decoder-libopus-guard bench-encoder-libopus-guard

# Libopus-relative decode performance guardrail on the official RFC 8251 bitstreams.
bench-decoder-libopus-guard: ensure-libopus ensure-testvectors
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=aggregate -paths=all -benchtime=$(BENCH_LIBOPUS_GUARD_TIME) -count=$(BENCH_LIBOPUS_GUARD_COUNT) -format=tsv -max-gopus-libopus-ratio=$(BENCH_LIBOPUS_GUARD_RATIO) -max-gopus-allocs-per-op=$(BENCH_LIBOPUS_GUARD_ALLOCS)

# Libopus-relative encoder performance guardrail across CELT, SILK, and Hybrid workloads.
bench-encoder-libopus-guard: ensure-libopus
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/encoderbenchcmp -cases=$(BENCH_ENCODER_LIBOPUS_GUARD_CASES) -benchtime=$(BENCH_LIBOPUS_GUARD_TIME) -count=$(BENCH_LIBOPUS_GUARD_COUNT) -format=tsv -max-gopus-libopus-ratio=$(BENCH_ENCODER_LIBOPUS_GUARD_RATIO) -max-gopus-allocs-per-op=$(BENCH_ENCODER_LIBOPUS_GUARD_ALLOCS)

# Decode the official RFC 8251 bitstreams with benchmark metrics per vector.
bench-testvectors: ensure-testvectors
	$(GO_WORK_ENV) $(GO) test $(PGO_FLAG) ./testvectors -run='^$$' -bench='^BenchmarkDecodeOfficialTestVectors$$' -benchmem -count=1

# Compare the same official bitstreams against pinned libopus and emit Markdown.
bench-testvectors-compare: ensure-libopus ensure-testvectors
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=$(BENCH_TESTVECTORS_COMPARE_CASES) -paths=$(BENCH_TESTVECTORS_COMPARE_PATHS) $(BENCH_TESTVECTORS_COMPARE_TIME_FLAG) -count=$(BENCH_TESTVECTORS_COMPARE_COUNT) -gopus-pgo=$(PGO_REPORT_PROFILE) -format=markdown

# Refresh the checked-in Markdown benchmark report.
bench-testvectors-report: ensure-libopus ensure-testvectors
	$(GO_WORK_ENV) $(GO) run $(PGO_FLAG) ./tools/testvectorbenchcmp -cases=$(BENCH_TESTVECTORS_COMPARE_CASES) -paths=$(BENCH_TESTVECTORS_COMPARE_PATHS) $(BENCH_TESTVECTORS_COMPARE_TIME_FLAG) -count=$(BENCH_TESTVECTORS_COMPARE_COUNT) -gopus-pgo=$(PGO_REPORT_PROFILE) -format=markdown -out docs/testvector-benchmarks.md

# Default production verification gate.
verify-production: ensure-libopus
	$(RUNNABLE_PARITY) -count=1 -timeout=25m
	$(MAKE) test-consumer-smoke
	$(MAKE) test-examples-smoke
	$(MAKE) test-dnn-blob-parity
	$(MAKE) test-dred-tag
	$(MAKE) test-qext-parity
	$(MAKE) test-unsupported-controls-tag
	$(MAKE) test-unsupported-controls-parity
	$(MAKE) bench-guard
	$(MAKE) bench-libopus-guard
	$(MAKE) test-race

# Extended production gate (includes fuzz + exhaustive fixture honesty).
verify-production-exhaustive: verify-production
	$(MAKE) test-fuzz-smoke
	$(MAKE) test-exhaustive
	$(MAKE) test-provenance

# Safety verification gate: strong existing checks first, then adversarial stress.
verify-safety: ensure-libopus
	$(MAKE) test-race
	$(MAKE) test-quality
	$(MAKE) test-exhaustive
	$(MAKE) bench-guard
	$(MAKE) bench-libopus-guard
	$(MAKE) test-assembly-safety
	$(MAKE) test-fuzz-safety
	$(MAKE) test-soak-safety
	$(MAKE) release-evidence

# Generate a release evidence bundle (gates + key benchmarks).
release-evidence: ensure-libopus
	./tools/gen_release_evidence.sh $(RELEASE_EVIDENCE_DIR)

# Local release preflight before pushing a public tag.
release-preflight:
	@test -n "$(TAG)" || { echo "TAG is required, for example: make release-preflight TAG=v0.1.0"; exit 1; }
	@case "$(TAG)" in \
		v[0-9]*.[0-9]*.[0-9]*) ;; \
		*) echo "TAG must look like v0.1.0"; exit 1 ;; \
	esac
	@test -f "docs/releases/$(TAG).md" || { echo "missing release notes: docs/releases/$(TAG).md"; exit 1; }
	@git diff --quiet --ignore-submodules -- && git diff --cached --quiet --ignore-submodules -- || { echo "working tree must be clean before release-preflight"; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || { echo "tag $(TAG) already exists locally"; exit 1; }
	$(MAKE) lint
	$(MAKE) verify-production-exhaustive
	$(MAKE) release-evidence
	@test -n "$$(find "$(RELEASE_EVIDENCE_DIR)" -maxdepth 1 -type f -name 'release-evidence-*.md' -print -quit)" || { echo "missing generated release evidence summary in $(RELEASE_EVIDENCE_DIR)"; exit 1; }
	@grep -q 'Overall result: PASS' "$$(find "$(RELEASE_EVIDENCE_DIR)" -maxdepth 1 -type f -name 'release-evidence-*.md' | sort | tail -n 1)" || { echo "latest release evidence summary did not pass"; exit 1; }

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo exists (fetch + build if missing).
ensure-libopus:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) ./tools/ensure_libopus.sh

# Ensure tmp_check/opus-$(LIBOPUS_VERSION)-qext/opus_demo exists with ENABLE_QEXT.
ensure-libopus-qext:
	LIBOPUS_VERSION=$(LIBOPUS_VERSION) LIBOPUS_ENABLE_QEXT=1 ./tools/ensure_libopus.sh

# Ensure the downloaded official RFC 8251 test-vector cache exists.
ensure-testvectors:
	@bash -c 'set -euo pipefail; \
		dir="testvectors/testdata/opus_testvectors"; \
		complete() { \
			for n in 01 02 03 04 05 06 07 08 09 10 11 12; do \
				for ext in bit dec; do \
					test -s "$$dir/testvector$$n.$$ext" || return 1; \
				done; \
			done; \
		}; \
		if ! complete; then \
			tmp=$$(mktemp -d); \
			trap "rm -rf \"$$tmp\"" EXIT; \
			archive="$$tmp/opus_testvectors-rfc8251.tar.gz"; \
			for url in "$(TEST_VECTOR_URL)" "$(TEST_VECTOR_FALLBACK_URL)"; do \
				echo "fetching official test vectors from $$url"; \
				if curl -fsSL --retry 3 --retry-delay 2 --connect-timeout 15 --max-time 180 "$$url" -o "$$archive"; then \
					fetched=1; \
					break; \
				fi; \
			done; \
			test "$${fetched:-}" = 1 || { echo "failed to fetch official test vectors"; exit 1; }; \
			rm -rf "$$dir"; \
			mkdir -p "$$dir"; \
			tar -xzf "$$archive" -C "$$tmp"; \
			find "$$tmp" -type f \( -name "testvector*.bit" -o -name "testvector*.dec" \) -exec cp {} "$$dir"/ \;; \
			complete || { echo "downloaded official test vectors are incomplete"; exit 1; }; \
		fi'
	cd testvectors && $(GO_WORK_ENV) $(GO) test . -run='^TestParseTestVectorBitstreams$$' -count=1

# Build pinned Linux CI image with codec/tooling dependencies.
docker-buildx-bootstrap:
	@docker buildx inspect $(DOCKER_BUILDER) >/dev/null 2>&1 || docker buildx create --name $(DOCKER_BUILDER) --driver docker-container >/dev/null
	@docker buildx inspect $(DOCKER_BUILDER) --bootstrap >/dev/null

# Build pinned Linux CI image with codec/tooling dependencies.
docker-build: docker-buildx-bootstrap
	@mkdir -p $(DOCKER_BUILDX_CACHE_DIR)
	@rm -rf $(DOCKER_BUILDX_CACHE_DIR)-new
	docker buildx build --builder $(DOCKER_BUILDER) --load --platform $(DOCKER_PLATFORM) --cache-from type=local,src=$(DOCKER_BUILDX_CACHE_DIR) --cache-to type=local,dest=$(DOCKER_BUILDX_CACHE_DIR)-new,mode=max --build-arg LIBOPUS_VERSION=$(LIBOPUS_VERSION) -f $(DOCKERFILE_CI) -t $(DOCKER_IMAGE) .
	@rm -rf $(DOCKER_BUILDX_CACHE_DIR)
	@mv $(DOCKER_BUILDX_CACHE_DIR)-new $(DOCKER_BUILDX_CACHE_DIR)

# Build image for exhaustive fixture-honesty checks (defaults to linux/amd64).
docker-build-exhaustive: docker-buildx-bootstrap
	@mkdir -p $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)
	@rm -rf $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new
	docker buildx build --builder $(DOCKER_BUILDER) --load --platform $(DOCKER_EXHAUSTIVE_PLATFORM) --cache-from type=local,src=$(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR) --cache-to type=local,dest=$(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new,mode=max --build-arg LIBOPUS_VERSION=$(LIBOPUS_VERSION) -f $(DOCKERFILE_CI) -t $(DOCKER_IMAGE) .
	@rm -rf $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)
	@mv $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)-new $(DOCKER_EXHAUSTIVE_BUILDX_CACHE_DIR)

# Run full test suite in cached Linux container (modules/build/libopus volumes).
docker-test: docker-build
	docker run --rm --platform $(DOCKER_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && go test ./... -count=1"

# Run exhaustive fixture honesty/provenance checks in cached Linux container.
docker-test-exhaustive: docker-build-exhaustive
	docker run --rm --platform $(DOCKER_EXHAUSTIVE_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
	$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && GOPUS_TEST_TIER=exhaustive go test ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo|TestEncoderVariantsFixtureHonestyWithOpusDemo|TestDecoderParityMatrixFixtureHonestyWithOpusDemo|TestDecoderLossFixtureHonestyWithOpusDemo|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1"

# Open an interactive shell with the same cached Docker environment.
docker-shell: docker-build
	docker run --rm -it --platform $(DOCKER_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		-e GOPUS_DISABLE_OPUSDEC=$(DOCKER_DISABLE_OPUSDEC) \
		-e GOPUS_DISABLE_OPUSENC=$(DOCKER_DISABLE_OPUSENC) \
		$(DOCKER_IMAGE) \
		bash

# Exhaustive tier includes fixture honesty checks against pinned tmp_check opus_demo/opusdec.
test-exhaustive: ensure-libopus
	$(GO_TEST_EXHAUSTIVE) ./testvectors -run 'TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo|TestEncoderVariantsFixtureHonestyWithOpusDemo|TestDecoderParityMatrixFixtureHonestyWithOpusDemo|TestDecoderLossFixtureHonestyWithOpusDemo|TestLongFrameReferenceFixtureHonestyWithLiveOpusdec' -count=1

# Exhaustive provenance audit for encoder variant parity.
test-provenance: ensure-libopus
	$(GO_TEST_EXHAUSTIVE) ./testvectors -run 'TestEncoderVariantProfileProvenanceAudit' -count=1

# Regenerate fixture files from tmp_check/opus-$(LIBOPUS_VERSION)/opus_demo.
fixtures-gen: ensure-libopus fixtures-gen-decoder fixtures-gen-decoder-loss fixtures-gen-encoder fixtures-gen-variants

fixtures-gen-decoder:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_decoder_matrix_fixture.go

fixtures-gen-decoder-loss:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_decoder_loss_fixture.go

fixtures-gen-encoder:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_encoder_packet_fixture.go

fixtures-gen-variants:
	$(GO_WORK_ENV) $(GO) run tools/gen_libopus_encoder_variants_fixture.go

# Regenerate amd64-specific fixture files in a cached linux/amd64 container.
fixtures-gen-amd64: docker-build-exhaustive
	docker run --rm --platform $(DOCKER_EXHAUSTIVE_PLATFORM) \
		-v "$(CURDIR):/workspace" \
		-v gopus-gomod:/go/pkg/mod \
		-v gopus-gobuild-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/root/.cache/go-build \
		-v gopus-libopus-$(DOCKER_EXHAUSTIVE_CACHE_SUFFIX):/workspace/tmp_check \
		-w /workspace \
		-e LIBOPUS_VERSION=$(LIBOPUS_VERSION) \
		$(DOCKER_IMAGE) \
		bash -c "make ensure-libopus && \
			GOPUS_DECODER_MATRIX_FIXTURE_OUT=testvectors/testdata/libopus_decoder_matrix_fixture_amd64.json go run tools/gen_libopus_decoder_matrix_fixture.go && \
			GOPUS_DECODER_LOSS_FIXTURE_OUT=testvectors/testdata/libopus_decoder_loss_fixture_amd64.json go run tools/gen_libopus_decoder_loss_fixture.go && \
			GOPUS_ENCODER_PACKETS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_packets_fixture_amd64.json go run tools/gen_libopus_encoder_packet_fixture.go && \
			GOPUS_ENCODER_VARIANTS_FIXTURE_OUT=testvectors/testdata/encoder_compliance_libopus_variants_fixture_amd64.json go run tools/gen_libopus_encoder_variants_fixture.go && \
			GOPUS_OPUSDEC_CROSSVAL_FIXTURE_OUT=celt/testdata/opusdec_crossval_fixture_amd64.json go run tools/gen_opusdec_crossval_fixture.go"

# Build with profile-guided optimization.
build:
	$(GO_WORK_ENV) $(GO) build $(PGO_FLAG) ./...

# Build without profile-guided optimization
build-nopgo:
	$(GO_WORK_ENV) $(GO) build -pgo=off ./...

# Regenerate default.pgo from representative public encode/decode hot-path benchmarks
pgo-generate:
	$(GO_WORK_ENV) $(GO) test $(PGO_GENERATE_FLAG) -run='^$$' -bench='$(PGO_BENCH)' -benchtime=$(PGO_BENCHTIME) -count=$(PGO_COUNT) -cpuprofile $(PGO_FILE) $(PGO_PKG)

# Refresh default.pgo then build with PGO enabled
pgo-build: pgo-generate build

# Remove local build/test artifacts generated during development.
clean:
	find . -maxdepth 1 -type f \( -name '*.test' -o -name '*.prof' -o -name '*.out' -o -name '*.o' -o -name '*.trace' -o -name 'coverage.out' -o -name 'coverage.html' \) -delete

# Remove downloaded official Opus test vectors cache.
clean-vectors:
	rm -rf testvectors/testdata/opus_testvectors/

# Run kernel-level benchmarks for CELT and SILK DSP functions.
bench-kernels:
	$(GO_WORK_ENV) $(GO) test -bench=. -benchmem -count=5 ./celt/ ./silk/
