/* Stateful oracle calling the selected, untouched libopus Custom API.
 * GCWS framing uses little-endian integers and IEEE binary32 PCM.
 * Each case retains one encoder and independent float/int16 decoders.
 * Record operations: 0 encode/decode, 1 loss, 2 reset then encode/decode.
 * Decoder inputs always come from the C encoder in this process.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "config.h"
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#include "opus_custom.h"
#include "opus_defines.h"
#include "celt.h"

static uint32_t read_u32(void) {
    unsigned char b[4];
    if (fread(b, 1, 4, stdin) != 4) exit(2);
    return (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
}
static void write_u32(uint32_t v) {
    unsigned char b[4] = {v, v>>8, v>>16, v>>24};
    if (fwrite(b, 1, 4, stdout) != 4) exit(3);
}
static float read_f32(void) {
    uint32_t bits=read_u32(); float value;
    memcpy(&value,&bits,4); return value;
}
static void write_f32(float value) {
    uint32_t bits; memcpy(&bits,&value,4); write_u32(bits);
}
static void write_i16(opus_int16 value) {
    uint16_t bits=(uint16_t)value;
    unsigned char b[2]={bits,bits>>8};
    if (fwrite(b,1,2,stdout)!=2) exit(3);
}
static void check_status(int status) {
    if (status!=OPUS_OK) {fprintf(stderr,"custom oracle control: %d\n",status);exit(4);}
}
int main(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin),_O_BINARY)==-1 || _setmode(_fileno(stdout),_O_BINARY)==-1) return 2;
#endif
    char magic[4];
    if (fread(magic, 1, 4, stdin) != 4 || memcmp(magic, "GCWS", 4)) return 2;
    uint32_t cases = read_u32();
    if (cases > 2048) return 2;
    if (fwrite("GCWS",1,4,stdout)!=4) return 3;
    write_u32(cases);
    for (uint32_t i=0; i<cases; i++) {
        int fs=read_u32(), n=read_u32(), channels=read_u32(), records=read_u32(), err;
        if (n<1 || n>2048 || channels<1 || channels>2 || records<1 || records>128) return 2;
        OpusCustomMode *m=opus_custom_mode_create(fs,n,&err);
        if (!m || err) return 4;
        OpusCustomEncoder *enc=opus_custom_encoder_create(m,channels,&err);
        if (!enc || err) return 4;
        OpusCustomDecoder *dec=opus_custom_decoder_create(m,channels,&err);
        if (!dec || err) return 4;
        OpusCustomDecoder *dec16=opus_custom_decoder_create(m,channels,&err);
        if (!dec16 || err) return 4;
        check_status(opus_custom_encoder_ctl(enc, OPUS_SET_VBR(0)));
        check_status(opus_custom_encoder_ctl(enc, OPUS_SET_VBR_CONSTRAINT(0)));
        check_status(opus_custom_encoder_ctl(enc, OPUS_SET_COMPLEXITY(9)));
        check_status(opus_custom_encoder_ctl(enc, OPUS_SET_LSB_DEPTH(16)));
        check_status(opus_custom_encoder_ctl(enc, CELT_SET_SIGNALLING(0)));
        check_status(opus_custom_decoder_ctl(dec, CELT_SET_SIGNALLING(0)));
        check_status(opus_custom_decoder_ctl(dec16, CELT_SET_SIGNALLING(0)));
        write_u32(records);
        for (int j=0;j<records;j++) {
            uint32_t op=read_u32(), max_bytes=read_u32();
            if (op>2 || max_bytes<2 || max_bytes>1275) return 2;
            float input[4096], output[4096];
            opus_int16 output16[4096];
            unsigned char packet[1275];
            for (int k=0;k<n*channels;k++) input[k]=read_f32();
            if (op==2) {
                check_status(opus_custom_encoder_ctl(enc,OPUS_RESET_STATE));
                check_status(opus_custom_decoder_ctl(dec,OPUS_RESET_STATE));
                check_status(opus_custom_decoder_ctl(dec16,OPUS_RESET_STATE));
            }
            int len=0;
            opus_uint32 enc_range=0, dec_range=0, short_range=0;
            if (op!=1) {
                len=opus_custom_encode_float(enc,input,n,packet,max_bytes);
                check_status(opus_custom_encoder_ctl(enc,OPUS_GET_FINAL_RANGE(&enc_range)));
            }
            write_u32(len);
            write_u32(enc_range);
            if (len<0) return 5;
            if (len && fwrite(packet,1,len,stdout)!=(size_t)len) return 3;
            int got=opus_custom_decode_float(dec,op==1?NULL:packet,len,output,n);
            int got16=opus_custom_decode(dec16,op==1?NULL:packet,len,output16,n);
            check_status(opus_custom_decoder_ctl(dec,OPUS_GET_FINAL_RANGE(&dec_range)));
            check_status(opus_custom_decoder_ctl(dec16,OPUS_GET_FINAL_RANGE(&short_range)));
            write_u32(got); write_u32(dec_range);
            if (got>0) for (int k=0;k<got*channels;k++) write_f32(output[k]);
            write_u32(got16); write_u32(short_range);
            if (got16>0) for (int k=0;k<got16*channels;k++) write_i16(output16[k]);
        }
        opus_custom_decoder_destroy(dec16);
        opus_custom_decoder_destroy(dec);
        opus_custom_encoder_destroy(enc);
        opus_custom_mode_destroy(m);
    }
    return 0;
}
