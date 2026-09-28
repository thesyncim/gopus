/* Generated custom-mode tables and transforms from the selected live C build. */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "config.h"
#include "opus_custom.h"
#include "celt/modes.h"
#include "celt/mdct.h"
#include "celt/kiss_fft.h"
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#endif
#if !defined(FIXED_POINT) || !defined(ENABLE_QEXT) || !defined(CUSTOM_MODES)
#error "custom fixed QEXT reference required"
#endif

static uint32_t read_u32(void) {
    unsigned char b[4];
    if (fread(b,1,4,stdin)!=4) exit(2);
    return (uint32_t)b[0] | (uint32_t)b[1]<<8 | (uint32_t)b[2]<<16 | (uint32_t)b[3]<<24;
}
static void write_u32(uint32_t v) {
    unsigned char b[4]={v,v>>8,v>>16,v>>24};
    if (fwrite(b,1,4,stdout)!=4) exit(3);
}

int main(void) {
#ifdef _WIN32
    if (_setmode(_fileno(stdin),_O_BINARY)==-1 || _setmode(_fileno(stdout),_O_BINARY)==-1) return 2;
#endif
    char magic[4];
    if (fread(magic,1,4,stdin)!=4 || memcmp(magic,"GCQI",4) || read_u32()!=1) return 2;
    int fs=read_u32(), frame=read_u32(), error;
    if (frame<40 || frame>2048) return 2;
    OpusCustomMode *mode=opus_custom_mode_create(fs,frame,&error);
    if (!mode || error) return 4;
    const mdct_lookup *l=&mode->mdct;
    if (l->n!=2*frame) return 5; /* This oracle selects generated modes. */
    opus_int32 *input=calloc(l->n,sizeof(*input));
    opus_int32 *work=calloc(l->n,sizeof(*work));
    opus_int32 *out=calloc(l->n,sizeof(*out));
    opus_int32 *back=calloc(l->n,sizeof(*back));
    if (!input || !work || !out || !back) return 6;
    for (int i=0;i<l->n;i++) input[i]=(int32_t)read_u32();
    if (fwrite("GCQO",1,4,stdout)!=4) return 3;
    write_u32(1); write_u32(l->n); write_u32(l->maxshift); write_u32(mode->overlap);
    int trig_count=l->n-(l->n>>(l->maxshift+1));
    for (int i=0;i<trig_count;i++) write_u32(l->trig[i]);
    for (int i=0;i<mode->overlap;i++) write_u32(mode->window[i]);
    for (int shift=0;shift<=l->maxshift;shift++) {
        const kiss_fft_state *st=l->kfft[shift];
        write_u32(st->nfft); write_u32(st->scale); write_u32(st->scale_shift); write_u32(st->shift);
        int stages=0;
        do { stages++; } while (stages<MAXFACTORS && st->factors[2*stages-1]!=1);
        write_u32(stages);
        for (int i=0;i<2*stages;i++) write_u32(st->factors[i]);
        for (int i=0;i<st->nfft;i++) write_u32(st->bitrev[i]);
    }
    for (int i=0;i<l->kfft[0]->nfft;i++) {
        write_u32(l->kfft[0]->twiddles[i].r); write_u32(l->kfft[0]->twiddles[i].i);
    }
    for (int shift=0;shift<=l->maxshift;shift++) {
        int count=l->n>>(shift+1);
        for (int strided=0;strided<2;strided++) {
            int stride=strided ? 1<<shift : 1;
            int out_count=stride*(count-1)+1;
            memcpy(work,input,l->n*sizeof(*work));
            memset(out,0,l->n*sizeof(*out));
            clt_mdct_forward_c(l,work,out,mode->window,mode->overlap,shift,stride,0);
            for (int i=0;i<out_count;i++) write_u32(out[i]);
            memcpy(back,input,l->n*sizeof(*back));
            clt_mdct_backward_c(l,out,back,mode->window,mode->overlap,shift,stride,0);
            for (int i=0;i<count+mode->overlap/2;i++) write_u32(back[i]);
        }
    }
    free(input); free(work); free(out); free(back);
    opus_custom_mode_destroy(mode);
    return 0;
}
